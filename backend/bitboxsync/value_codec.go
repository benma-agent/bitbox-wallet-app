// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/notes"
	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
)

const (
	txIDEncodingRaw   = byte(0x00)
	txIDEncodingHex   = byte(0x01)
	txIDEncoding0xHex = byte(0x02)

	compressedTxIDPayloadLen = 32
	compressedTxIDLen        = 1 + compressedTxIDPayloadLen
	maxRawTxIDLen            = math.MaxUint8
)

// Packed sync values are fixed per collection. Do not evolve these formats in
// place; add new collections/items instead when syncing additional data.
//
// txNotesBucket:
//   - uvarint entry count
//   - entries sorted by transaction ID:
//   - tx ID: 0x00, uint8 length, raw UTF-8 bytes
//   - tx ID: 0x01, 32 bytes lowercase hex transaction ID
//   - tx ID: 0x02, 32 bytes lowercase hex transaction ID with "0x" prefix
//   - uint64 big-endian Unix nanoseconds modified timestamp, 0 for unset
//   - uvarint note length, UTF-8 note bytes
//
// accountNameValue:
//   - uint64 big-endian Unix nanoseconds modified timestamp, 0 for unset
//   - uvarint name length, UTF-8 name bytes
type fixedValueCodec[T any] struct {
	encode func(T) ([]byte, error)
	decode func([]byte) (T, error)
}

// Encode implements syncclient.Codec.
func (c fixedValueCodec[T]) Encode(value T) ([]byte, error) {
	return c.encode(value)
}

// Decode implements syncclient.Codec.
func (c fixedValueCodec[T]) Decode(payload []byte) (T, error) {
	return c.decode(payload)
}

func txNotesBucketCodec() fixedValueCodec[txNotesBucket] {
	return fixedValueCodec[txNotesBucket]{
		encode: encodeTxNotesBucket,
		decode: decodeTxNotesBucket,
	}
}

func accountNameValueCodec() fixedValueCodec[accountNameValue] {
	return fixedValueCodec[accountNameValue]{
		encode: encodeAccountNameValue,
		decode: decodeAccountNameValue,
	}
}

func encodeTxNotesBucket(value txNotesBucket) ([]byte, error) {
	txIDs := make([]string, 0, len(value))
	for txID := range value {
		txIDs = append(txIDs, txID)
	}
	sort.Strings(txIDs)

	out := binary.AppendUvarint(nil, uint64(len(txIDs)))
	for _, txID := range txIDs {
		entry := value[txID]
		packedTxID, err := CompressInternalTxID(txID)
		if err != nil {
			return nil, err
		}
		modifiedAt, err := timeToTimestampNanos(entry.ModifiedAt)
		if err != nil {
			return nil, err
		}
		if len(entry.Note) > notes.MaxNoteLen {
			return nil, errp.Newf("Length of note must be smaller than %d. Got %d", notes.MaxNoteLen, len(entry.Note))
		}
		if !utf8.ValidString(entry.Note) {
			return nil, errp.New("transaction note is not valid UTF-8")
		}
		out = append(out, packedTxID...)
		out = binary.BigEndian.AppendUint64(out, modifiedAt)
		out = binary.AppendUvarint(out, uint64(len(entry.Note)))
		out = append(out, entry.Note...)
	}
	return out, nil
}

func decodeTxNotesBucket(payload []byte) (txNotesBucket, error) {
	decoder := valueDecoder{payload: payload}
	entryCount, err := decoder.readUvarint()
	if err != nil {
		return nil, err
	}
	out := txNotesBucket{}
	for i := uint64(0); i < entryCount; i++ {
		txID, err := decoder.readPackedInternalTxID()
		if err != nil {
			return nil, err
		}
		if _, ok := out[txID]; ok {
			return nil, errp.New("duplicate transaction note tx id")
		}
		modifiedAt, err := decoder.readTimestamp()
		if err != nil {
			return nil, err
		}
		noteLen, err := decoder.readUvarint()
		if err != nil {
			return nil, err
		}
		if noteLen > notes.MaxNoteLen {
			return nil, errp.Newf("Length of note must be smaller than %d. Got %d", notes.MaxNoteLen, noteLen)
		}
		noteBytes, err := decoder.readBytes(noteLen)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(noteBytes) {
			return nil, errp.New("transaction note is not valid UTF-8")
		}
		out[txID] = txNoteEntry{
			Note:       string(noteBytes),
			ModifiedAt: modifiedAt,
		}
	}
	if err := decoder.done(); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeAccountNameValue(value accountNameValue) ([]byte, error) {
	if err := validateAccountNameValue(value); err != nil {
		return nil, err
	}
	if !utf8.ValidString(value.Name) {
		return nil, errp.New("account name is not valid UTF-8")
	}
	modifiedAt, err := timeToTimestampNanos(value.ModifiedAt)
	if err != nil {
		return nil, err
	}
	out := binary.BigEndian.AppendUint64(nil, modifiedAt)
	out = binary.AppendUvarint(out, uint64(len(value.Name)))
	out = append(out, value.Name...)
	return out, nil
}

func decodeAccountNameValue(payload []byte) (accountNameValue, error) {
	decoder := valueDecoder{payload: payload}
	modifiedAt, err := decoder.readTimestamp()
	if err != nil {
		return accountNameValue{}, err
	}
	nameLen, err := decoder.readUvarint()
	if err != nil {
		return accountNameValue{}, err
	}
	nameBytes, err := decoder.readBytes(nameLen)
	if err != nil {
		return accountNameValue{}, err
	}
	if err := decoder.done(); err != nil {
		return accountNameValue{}, err
	}
	if !utf8.Valid(nameBytes) {
		return accountNameValue{}, errp.New("account name is not valid UTF-8")
	}
	value := accountNameValue{
		ModifiedAt: modifiedAt,
		Name:       string(nameBytes),
	}
	if err := validateAccountNameValue(value); err != nil {
		return accountNameValue{}, err
	}
	return value, nil
}

// CompressInternalTxID encodes common internal transaction IDs compactly.
func CompressInternalTxID(internalTxID string) ([]byte, error) {
	if internalTxID == "" {
		return nil, errp.New("transaction note tx id cannot be empty")
	}
	if isLowerHex(internalTxID) {
		return compressHexTxID(txIDEncodingHex, internalTxID)
	}
	if len(internalTxID) == 2+64 && internalTxID[:2] == "0x" && isLowerHex(internalTxID[2:]) {
		return compressHexTxID(txIDEncoding0xHex, internalTxID[2:])
	}
	if len(internalTxID) > maxRawTxIDLen {
		return nil, errp.New("transaction note raw tx id is too long")
	}
	return append([]byte{txIDEncodingRaw, byte(len(internalTxID))}, internalTxID...), nil
}

// UncompressInternalTxID decodes an internal transaction ID encoded by CompressInternalTxID.
func UncompressInternalTxID(encoded []byte) (string, error) {
	decoder := valueDecoder{payload: encoded}
	txID, err := decoder.readPackedInternalTxID()
	if err != nil {
		return "", err
	}
	if err := decoder.done(); err != nil {
		return "", err
	}
	return txID, nil
}

func compressHexTxID(tag byte, hexTxID string) ([]byte, error) {
	out := make([]byte, compressedTxIDLen)
	out[0] = tag
	if _, err := hex.Decode(out[1:], []byte(hexTxID)); err != nil {
		return nil, errp.WithStack(err)
	}
	return out, nil
}

func isLowerHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') {
			continue
		}
		return false
	}
	return true
}

type valueDecoder struct {
	payload []byte
	offset  int
}

func (d *valueDecoder) readByte() (byte, error) {
	if d.offset >= len(d.payload) {
		return 0, errp.New("packed value ended unexpectedly")
	}
	value := d.payload[d.offset]
	d.offset++
	return value, nil
}

func (d *valueDecoder) readBytes(length uint64) ([]byte, error) {
	if length > uint64(len(d.payload)-d.offset) {
		return nil, errp.New("packed value ended unexpectedly")
	}
	start := d.offset
	d.offset += int(length)
	return d.payload[start:d.offset], nil
}

func (d *valueDecoder) readUvarint() (uint64, error) {
	if d.offset >= len(d.payload) {
		return 0, errp.New("packed value uvarint is missing")
	}
	value, n := binary.Uvarint(d.payload[d.offset:])
	switch {
	case n > 0:
		d.offset += n
		return value, nil
	case n == 0:
		return 0, errp.New("packed value uvarint is incomplete")
	default:
		return 0, errp.New("packed value uvarint overflows uint64")
	}
}

func (d *valueDecoder) readUint64() (uint64, error) {
	valueBytes, err := d.readBytes(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(valueBytes), nil
}

func (d *valueDecoder) readTimestamp() (time.Time, error) {
	value, err := d.readUint64()
	if err != nil {
		return time.Time{}, err
	}
	return timestampNanosToTime(value)
}

func (d *valueDecoder) readPackedInternalTxID() (string, error) {
	tag, err := d.readByte()
	if err != nil {
		return "", err
	}
	switch tag {
	case txIDEncodingRaw:
		rawLen, err := d.readByte()
		if err != nil {
			return "", err
		}
		if rawLen == 0 {
			return "", errp.New("transaction note raw tx id is empty")
		}
		raw, err := d.readBytes(uint64(rawLen))
		if err != nil {
			return "", err
		}
		return string(raw), nil
	case txIDEncodingHex:
		return d.readHexTxID("")
	case txIDEncoding0xHex:
		return d.readHexTxID("0x")
	default:
		return "", errp.New("unknown transaction note tx id encoding")
	}
}

func (d *valueDecoder) readHexTxID(prefix string) (string, error) {
	compressed, err := d.readBytes(compressedTxIDPayloadLen)
	if err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(compressed), nil
}

func (d *valueDecoder) done() error {
	if d.offset != len(d.payload) {
		return errp.New("packed value has trailing bytes")
	}
	return nil
}

func timeToTimestampNanos(value time.Time) (uint64, error) {
	if value.IsZero() {
		return 0, nil
	}
	value = value.UTC()
	if value.Before(time.Unix(0, 0)) {
		return 0, errp.New("timestamp is before unix epoch")
	}
	if value.Equal(time.Unix(0, 0)) {
		return 0, errp.New("timestamp at unix epoch is reserved")
	}
	if value.After(time.Unix(0, math.MaxInt64)) {
		return 0, errp.New("timestamp exceeds unix nanosecond range")
	}
	return uint64(value.UnixNano()), nil
}

func timestampNanosToTime(value uint64) (time.Time, error) {
	if value == 0 {
		return time.Time{}, nil
	}
	if value > math.MaxInt64 {
		return time.Time{}, errp.New("timestamp exceeds unix nanosecond range")
	}
	return time.Unix(0, int64(value)).UTC(), nil
}

// SPDX-License-Identifier: Apache-2.0

package bitboxsync

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/BitBoxSwiss/bitbox-wallet-app/backend/accounts/notes"
	"github.com/stretchr/testify/require"
)

func TestTxNotesBucketCodecFixtures(t *testing.T) {
	hexTxID := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	ethTxID := "0x202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
	rawTxID := "raw-id"
	original := txNotesBucket{
		hexTxID: {
			Note:       "hex",
			ModifiedAt: txNoteTestModifiedAt,
		},
		ethTxID: {
			Note:       "",
			ModifiedAt: txNoteTestModifiedAt.Add(time.Second),
		},
		rawTxID: {
			Note:       "raw",
			ModifiedAt: time.Time{},
		},
	}

	encoded, err := txNotesBucketCodec().Encode(original)
	require.NoError(t, err)
	expected := mustDecodeHex(t, `
		03
		01 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f
		18ae7bd0e5fe8000
		03 686578
		02 202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f
		18ae7bd121994a00
		00
		00 06 7261772d6964
		0000000000000000
		03 726177
	`)
	require.Equal(t, expected, encoded)

	decoded, err := txNotesBucketCodec().Decode(expected)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}

func TestTxNotesBucketCodecRoundtrip(t *testing.T) {
	hexTxID := strings.Repeat("ab", 32)
	ethTxID := "0x" + strings.Repeat("cd", 32)
	original := txNotesBucket{
		hexTxID: {
			Note:       "hex note",
			ModifiedAt: txNoteTestModifiedAt,
		},
		ethTxID: {
			Note:       "eth note",
			ModifiedAt: txNoteTestModifiedAt.Add(time.Second),
		},
		"internal-id": {
			Note:       "",
			ModifiedAt: txNoteTestModifiedAt.Add(2 * time.Second),
		},
	}

	codec := txNotesBucketCodec()
	encoded, err := codec.Encode(original)
	require.NoError(t, err)
	decoded, err := codec.Decode(encoded)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}

func TestTxNotesBucketCodecDeterministic(t *testing.T) {
	first := txNotesBucket{
		"tx-c": txNoteEntryForTest("note c"),
		"tx-a": txNoteEntryForTest("note a"),
		"tx-b": txNoteEntryForTest("note b"),
	}
	second := txNotesBucket{
		"tx-b": txNoteEntryForTest("note b"),
		"tx-c": txNoteEntryForTest("note c"),
		"tx-a": txNoteEntryForTest("note a"),
	}

	codec := txNotesBucketCodec()
	firstEncoded, err := codec.Encode(first)
	require.NoError(t, err)
	secondEncoded, err := codec.Encode(second)
	require.NoError(t, err)
	require.Equal(t, firstEncoded, secondEncoded)
}

func TestTxNotesBucketCodecRejectsMalformedPayloads(t *testing.T) {
	txID, err := CompressInternalTxID("tx-id")
	require.NoError(t, err)
	validEntry := append([]byte{0x01}, txID...)
	validEntry = binary.BigEndian.AppendUint64(validEntry, uint64(txNoteTestModifiedAt.UnixNano()))
	validEntry = binary.AppendUvarint(validEntry, uint64(len("note")))
	validEntry = append(validEntry, "note"...)

	for _, payload := range [][]byte{
		nil,
		{0x01, 0x03},
		append(append([]byte{}, validEntry...), 0x00),
		append(append([]byte{}, validEntry...), append(txID, binary.BigEndian.AppendUint64(nil, uint64(txNoteTestModifiedAt.UnixNano()))...)...),
	} {
		_, err := txNotesBucketCodec().Decode(payload)
		require.Error(t, err)
	}
}

func TestTxNotesBucketCodecRejectsDuplicateTxID(t *testing.T) {
	txID, err := CompressInternalTxID("tx-id")
	require.NoError(t, err)
	entry := append([]byte{}, txID...)
	entry = binary.BigEndian.AppendUint64(entry, uint64(txNoteTestModifiedAt.UnixNano()))
	entry = binary.AppendUvarint(entry, uint64(len("note")))
	entry = append(entry, "note"...)
	payload := binary.AppendUvarint(nil, 2)
	payload = append(payload, entry...)
	payload = append(payload, entry...)

	_, err = txNotesBucketCodec().Decode(payload)
	require.Error(t, err)
}

func TestTxNotesBucketCodecRejectsInvalidValues(t *testing.T) {
	_, err := txNotesBucketCodec().Encode(txNotesBucket{
		"tx-id": {
			Note:       string([]byte{0xff}),
			ModifiedAt: txNoteTestModifiedAt,
		},
	})
	require.Error(t, err)

	_, err = txNotesBucketCodec().Encode(txNotesBucket{
		"tx-id": {
			Note:       "note",
			ModifiedAt: time.Unix(-1, 0),
		},
	})
	require.Error(t, err)

	_, err = txNotesBucketCodec().Encode(txNotesBucket{
		"tx-id": {
			Note:       strings.Repeat("x", notes.MaxNoteLen+1),
			ModifiedAt: txNoteTestModifiedAt,
		},
	})
	require.Error(t, err)
}

func TestAccountNameValueCodecFixtures(t *testing.T) {
	original := accountNameValueWithName("Vault", txNoteTestModifiedAt)
	encoded, err := accountNameValueCodec().Encode(original)
	require.NoError(t, err)
	expected := mustDecodeHex(t, `
		18ae7bd0e5fe8000
		05 5661756c74
	`)
	require.Equal(t, expected, encoded)

	decoded, err := accountNameValueCodec().Decode(expected)
	require.NoError(t, err)
	require.Equal(t, original, decoded)
}

func TestAccountNameValueCodecFixtureBoundaryLength(t *testing.T) {
	name := strings.Repeat("x", 128)
	encoded, err := accountNameValueCodec().Encode(accountNameValueWithName(name, time.Time{}))
	require.NoError(t, err)
	expected := mustDecodeHex(t, "0000000000000000 8001 "+strings.Repeat("78", 128))
	require.Equal(t, expected, encoded)
}

func TestAccountNameValueCodecRoundtrip(t *testing.T) {
	codec := accountNameValueCodec()
	for _, original := range []accountNameValue{
		accountNameValueWithName("Bitcoin", time.Time{}),
		accountNameValueWithName("Savings", txNoteTestModifiedAt),
	} {
		encoded, err := codec.Encode(original)
		require.NoError(t, err)
		decoded, err := codec.Decode(encoded)
		require.NoError(t, err)
		require.Equal(t, original, decoded)
	}
}

func TestAccountNameValueCodecRejectsMalformedPayloads(t *testing.T) {
	valid := binary.BigEndian.AppendUint64(nil, uint64(txNoteTestModifiedAt.UnixNano()))
	valid = binary.AppendUvarint(valid, uint64(len("Bitcoin")))
	valid = append(valid, "Bitcoin"...)

	for _, payload := range [][]byte{
		nil,
		{0x00},
		append(append([]byte{}, valid...), 0x00),
		binary.BigEndian.AppendUint64(binary.AppendUvarint(nil, 1), uint64(txNoteTestModifiedAt.UnixNano())),
		append(binary.BigEndian.AppendUint64(nil, uint64(txNoteTestModifiedAt.UnixNano())), 0x01, 0xff),
	} {
		_, err := accountNameValueCodec().Decode(payload)
		require.Error(t, err)
	}
}

func TestAccountNameValueCodecRejectsInvalidTimestamps(t *testing.T) {
	_, err := accountNameValueCodec().Encode(accountNameValueWithName(
		"Pre Epoch",
		time.Unix(-1, 0),
	))
	require.Error(t, err)

	_, err = accountNameValueCodec().Encode(accountNameValueWithName(
		"Epoch",
		time.Unix(0, 0),
	))
	require.Error(t, err)

	encoded := binary.BigEndian.AppendUint64(nil, uint64(math.MaxInt64)+1)
	encoded = binary.AppendUvarint(encoded, uint64(len("Overflow")))
	encoded = append(encoded, "Overflow"...)
	_, err = accountNameValueCodec().Decode(encoded)
	require.Error(t, err)
}

func TestInternalTxIDCompressionFixtures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		txID        string
		expectedHex string
	}{
		{
			name:        "lowercase hex",
			txID:        "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			expectedHex: "01 000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		},
		{
			name:        "lowercase 0x hex",
			txID:        "0x202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f",
			expectedHex: "02 202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f",
		},
		{
			name:        "raw",
			txID:        "raw-id",
			expectedHex: "00 06 7261772d6964",
		},
		{
			name:        "max raw length",
			txID:        strings.Repeat("x", maxRawTxIDLen),
			expectedHex: "00 ff " + strings.Repeat("78", maxRawTxIDLen),
		},
		{
			name:        "raw invalid UTF-8 bytes",
			txID:        string([]byte{0xff}),
			expectedHex: "00 01 ff",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := mustDecodeHex(t, tc.expectedHex)
			encoded, err := CompressInternalTxID(tc.txID)
			require.NoError(t, err)
			require.Equal(t, expected, encoded)

			decoded, err := UncompressInternalTxID(expected)
			require.NoError(t, err)
			require.Equal(t, tc.txID, decoded)
		})
	}
}

func TestInternalTxIDCompression(t *testing.T) {
	hexTxID := strings.Repeat("ab", 32)
	ethTxID := "0x" + strings.Repeat("cd", 32)
	rawTxID := "AB" + strings.Repeat("ab", 31)

	for _, tc := range []struct {
		name     string
		txID     string
		wantTag  byte
		wantSize int
	}{
		{
			name:     "lowercase hex",
			txID:     hexTxID,
			wantTag:  txIDEncodingHex,
			wantSize: compressedTxIDLen,
		},
		{
			name:     "lowercase 0x hex",
			txID:     ethTxID,
			wantTag:  txIDEncoding0xHex,
			wantSize: compressedTxIDLen,
		},
		{
			name:     "raw fallback",
			txID:     rawTxID,
			wantTag:  txIDEncodingRaw,
			wantSize: 2 + len(rawTxID),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := CompressInternalTxID(tc.txID)
			require.NoError(t, err)
			require.Len(t, encoded, tc.wantSize)
			require.Equal(t, tc.wantTag, encoded[0])

			decoded, err := UncompressInternalTxID(encoded)
			require.NoError(t, err)
			require.Equal(t, tc.txID, decoded)
		})
	}
}

func TestInternalTxIDCompressionRejectsTooLongRawIDs(t *testing.T) {
	_, err := CompressInternalTxID(strings.Repeat("x", maxRawTxIDLen+1))
	require.Error(t, err)
}

func TestInternalTxIDDecompressionRejectsMalformedValues(t *testing.T) {
	for _, encoded := range [][]byte{
		nil,
		{0x03},
		{txIDEncodingRaw},
		{txIDEncodingRaw, 0x00},
		{txIDEncodingRaw, 0x02, 'a'},
		{txIDEncodingHex, 0x00},
		{txIDEncoding0xHex, 0x00},
		append(append([]byte{}, mustCompressInternalTxID(t, "tx-id")...), 0x00),
	} {
		_, err := UncompressInternalTxID(encoded)
		require.Error(t, err)
	}
}

func mustCompressInternalTxID(t *testing.T, txID string) []byte {
	t.Helper()
	encoded, err := CompressInternalTxID(txID)
	require.NoError(t, err)
	return encoded
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	value = strings.NewReplacer(" ", "", "\n", "", "\t", "").Replace(value)
	decoded, err := hex.DecodeString(value)
	require.NoError(t, err)
	return decoded
}

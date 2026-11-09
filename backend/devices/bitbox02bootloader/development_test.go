// SPDX-License-Identifier: Apache-2.0

package bitbox02bootloader

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BitBoxSwiss/bitbox02-api-go/api/bootloader"
	"github.com/BitBoxSwiss/bitbox02-api-go/api/common"
	"github.com/BitBoxSwiss/bitbox02-api-go/util/semver"
	"github.com/stretchr/testify/require"
)

type developmentBootloaderMock struct {
	mu              sync.Mutex
	product         common.Product
	version         *semver.SemVer
	stage0          uint16
	firmwareVersion uint32
	keysVersion     uint32
	firmware        []byte
	sigdata         []byte
	writes          []byte
	rebooted        chan struct{}
}

func (mock *developmentBootloaderMock) hash() []byte {
	var preimage []byte
	if mock.version.AtLeast(semver.NewSemVer(1, 2, 3)) {
		preimage = append(preimage, 1)
	}
	if mock.version.AtLeast(semver.NewSemVer(1, 2, 0)) {
		product := map[common.Product]uint16{
			common.ProductBitBox02Multi: 1, common.ProductBitBox02BTCOnly: 2,
			common.ProductBitBox02PlusMulti: 3, common.ProductBitBox02PlusBTCOnly: 4,
		}[mock.product]
		preimage = binary.LittleEndian.AppendUint16(preimage, product)
	}
	preimage = binary.LittleEndian.AppendUint32(preimage, mock.firmwareVersion)
	preimage = append(preimage, mock.firmware...)
	preimage = append(preimage, bytes.Repeat([]byte{0xff}, 884736-len(mock.firmware))...)
	hash := sha256.Sum256(preimage)
	if !mock.version.AtLeast(semver.NewSemVer(1, 2, 0)) {
		hash = sha256.Sum256(hash[:])
	}
	return hash[:]
}

func (mock *developmentBootloaderMock) Query(msg []byte) ([]byte, error) {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	response := []byte{msg[0], 0}
	switch msg[0] {
	case 'v':
		response = binary.LittleEndian.AppendUint32(response, mock.firmwareVersion)
		response = binary.LittleEndian.AppendUint32(response, mock.keysVersion)
		if mock.stage0 != 0 {
			response = binary.LittleEndian.AppendUint16(response, mock.stage0)
		}
	case 'h':
		response = append(response, mock.hash()...)
		response = append(response, make([]byte, 32)...)
	case 'e':
		mock.writes = append(mock.writes, 'e')
		mock.firmware = nil
	case 'w':
		mock.writes = append(mock.writes, 'w')
		mock.firmware = append(mock.firmware, msg[2:]...)
	case 's':
		mock.writes = append(mock.writes, 's')
		mock.sigdata = bytes.Clone(msg[1:])
		mock.keysVersion = binary.LittleEndian.Uint32(msg[1:5])
		mock.firmwareVersion = binary.LittleEndian.Uint32(msg[389:393])
	default:
		return nil, fmt.Errorf("unexpected command %q", msg[0])
	}
	return response, nil
}

func (mock *developmentBootloaderMock) SendFrame(msg string) error {
	if msg != "r" {
		return fmt.Errorf("unexpected frame %q", msg)
	}
	close(mock.rebooted)
	return nil
}

func (mock *developmentBootloaderMock) Close() {}

// Only a completed, recognized development updater may continue automatically to signed firmware.
func TestDevelopmentUpgradeSequence(t *testing.T) {
	for product := range bundledFirmwares {
		upgrader, err := developmentUpgrader(product)
		require.NoError(t, err)
		latest, err := bundledFirmware(product)
		require.NoError(t, err)
		signed, err := latest.signedBinary()
		require.NoError(t, err)
		_, expectedSigdata, regular, err := bootloader.ParseSignedFirmware(signed)
		require.NoError(t, err)
		for _, test := range []struct {
			name       string
			bootloader string
			stage0     uint16
			counter    uint32
			installed  []byte
			manual     bool
			result     string
		}{
			{"legacy", "1.0.0", 0, 35, []byte("custom"), true, "unsigned"},
			{"legacy-empty", "1.0.0", 0, 0, nil, true, "unsigned"},
			{"split", "1.2.2", 0, 54, []byte("custom"), true, "unsigned"},
			{"counter-56", "1.2.2", 0, 56, []byte("custom"), true, "unsigned"},
			{"old-bootloader-pending", "1.2.2", 0, 54, upgrader, true, "reboot"},
			{"new-stage1-old-stage0", "1.2.4", 2, 54, upgrader, false, "none"},
			{"run-after-premature-entry", "1.2.4", 2, 54, upgrader, true, "reboot"},
			{"unknown-stage0", "1.2.4", 0, 54, upgrader, false, "none"},
			{"resume-legacy-counter", "1.2.4", 3, 35, upgrader, false, "signed"},
			{"resume-v9270", "1.2.4", 3, 54, upgrader, false, "signed"},
			{"resume-latest-counter", "1.2.4", 3, 57, upgrader, false, "signed"},
			{"custom-no-auto-upgrade", "1.2.4", 3, 56, []byte("custom"), false, "none"},
			{"custom-explicit-upgrade", "1.2.4", 3, 56, []byte("custom"), true, "signed"},
			{"interrupted-no-auto-upgrade", "1.2.4", 3, 56, upgrader[:4096], false, "none"},
			{"interrupted-retry", "1.2.4", 3, 56, upgrader[:4096], true, "signed"},
			{"latest-no-auto-upgrade", "1.2.4", 3, 57, regular, false, "none"},
		} {
			t.Run(fmt.Sprintf("%s/%s", product, test.name), func(t *testing.T) {
				version, err := semver.NewSemVerFromString(test.bootloader)
				require.NoError(t, err)
				mock := &developmentBootloaderMock{
					product: product, version: version, stage0: test.stage0,
					firmwareVersion: test.counter, keysVersion: 4,
					firmware: bytes.Clone(test.installed), rebooted: make(chan struct{}),
				}
				device := NewDevice("dev", version, product, mock, bootloader.WithDevelopment(true))
				info, err := device.Info()
				require.NoError(t, err)
				require.Equal(t, len(test.installed) == 0, info.Erased)
				require.Equal(t, test.name != "latest-no-auto-upgrade", info.CanUpgrade)
				if test.manual {
					require.NoError(t, device.UpgradeFirmware())
				} else {
					require.NoError(t, device.Init(false))
				}
				if test.result == "none" {
					select {
					case <-mock.rebooted:
						t.Fatal("unexpected automatic upgrade")
					case <-time.After(20 * time.Millisecond):
					}
				} else {
					select {
					case <-mock.rebooted:
					case <-time.After(3 * time.Second):
						t.Fatal("upgrade did not reboot")
					}
				}
				mock.mu.Lock()
				defer mock.mu.Unlock()
				switch test.result {
				case "none", "reboot":
					require.Empty(t, mock.writes)
					require.Equal(t, test.counter, mock.firmwareVersion)
				case "unsigned":
					require.Empty(t, mock.sigdata)
					require.Equal(t, test.counter, mock.firmwareVersion)
					require.Equal(t, upgrader, mock.firmware[:len(upgrader)])
					require.True(t, device.Status().AdditionalUpgradeFollows)
				case "signed":
					require.Equal(t, expectedSigdata, mock.sigdata)
					require.Equal(t, uint32(57), mock.firmwareVersion)
					require.Equal(t, regular, mock.firmware[:len(regular)])
					require.False(t, device.Status().AdditionalUpgradeFollows)
				}
			})
		}
	}
}

// Refuse a sequence that cannot finish without lowering firmware or signing-key counters.
func TestDevelopmentUpgradeHighCounters(t *testing.T) {
	for _, counters := range [][2]uint32{{58, 4}, {54, 5}} {
		mock := &developmentBootloaderMock{
			product: common.ProductBitBox02Multi, version: semver.NewSemVer(1, 2, 2),
			firmwareVersion: counters[0], keysVersion: counters[1], rebooted: make(chan struct{}),
		}
		device := NewDevice("dev", mock.version, mock.product, mock, bootloader.WithDevelopment(true))
		_, err := device.Info()
		require.Error(t, err)
		require.Error(t, device.UpgradeFirmware())
		require.Empty(t, mock.writes)
	}
}

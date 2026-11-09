// SPDX-License-Identifier: Apache-2.0

package usb

import (
	"io"
	"testing"

	"github.com/BitBoxSwiss/bitbox02-api-go/api/common"
	"github.com/stretchr/testify/require"
)

type bootloaderInfoMock struct {
	serial    string
	bluetooth bool
	opened    bool
}

func (info *bootloaderInfoMock) IsBluetooth() bool { return info.bluetooth }
func (info *bootloaderInfoMock) VendorID() int     { return bitbox02VendorID }
func (info *bootloaderInfoMock) ProductID() int    { return bitbox02ProductID }
func (info *bootloaderInfoMock) UsagePage() int    { return 0xffff }
func (info *bootloaderInfoMock) Interface() int    { return 0 }
func (info *bootloaderInfoMock) Serial() string    { return info.serial }
func (info *bootloaderInfoMock) Product() string {
	return common.BootloaderDeviceProductStringBitBox02Multi
}
func (info *bootloaderInfoMock) Identifier() string { return "bootloader-test" }
func (info *bootloaderInfoMock) Open() (io.ReadWriteCloser, error) {
	info.opened = true
	return bootloaderHIDMock{}, nil
}

type bootloaderHIDMock struct{}

func (bootloaderHIDMock) Read([]byte) (int, error)       { return 0, io.EOF }
func (bootloaderHIDMock) Write(data []byte) (int, error) { return len(data), nil }
func (bootloaderHIDMock) Close() error                   { return nil }

// USB discovery must preserve the development marker when constructing the bootloader API.
func TestMakeBitBox02BootloaderDevelopment(t *testing.T) {
	manager := NewManager("", nil, nil, nil)
	for _, test := range []struct {
		serial      string
		development bool
	}{
		{"bb02.bl:v1.0.0-dev", true},
		{"v1.2.4-pre+abc.dev", true},
		{"v1.2.4", false},
		{"v1.2.4+device", false},
	} {
		t.Run(test.serial, func(t *testing.T) {
			info := &bootloaderInfoMock{serial: test.serial}
			device, err := manager.makeBitBox02Bootloader(info)
			require.NoError(t, err)
			defer device.Close()
			require.Equal(t, test.development, device.IsDevelopment())
		})
	}
}

// Development upgrades require USB and must reject unsupported transport before opening it.
func TestMakeBitBox02BootloaderDevelopmentBluetooth(t *testing.T) {
	manager := NewManager("", nil, nil, nil)
	info := &bootloaderInfoMock{serial: "v1.2.4+dev", bluetooth: true}
	_, err := manager.makeBitBox02Bootloader(info)
	require.Error(t, err)
	require.False(t, info.opened)
}

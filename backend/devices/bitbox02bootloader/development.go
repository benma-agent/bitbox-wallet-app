// SPDX-License-Identifier: Apache-2.0

package bitbox02bootloader

import (
	_ "embed"
	"encoding/binary"

	"github.com/BitBoxSwiss/bitbox-wallet-app/util/errp"
	"github.com/BitBoxSwiss/bitbox02-api-go/api/bootloader"
	"github.com/BitBoxSwiss/bitbox02-api-go/api/common"
	"github.com/BitBoxSwiss/bitbox02-api-go/util/semver"
)

//go:embed assets/blupgrade-bitbox02-multi-development.stage1-v1.2.4.bin.gz
var developmentUpgradeMulti []byte

//go:embed assets/blupgrade-bitbox02-btconly-development.stage1-v1.2.4.bin.gz
var developmentUpgradeBTCOnly []byte

//go:embed assets/blupgrade-bitbox02nova-multi-development.stage1-v1.2.4.bin.gz
var developmentUpgradeNovaMulti []byte

//go:embed assets/blupgrade-bitbox02nova-btconly-development.stage1-v1.2.4.bin.gz
var developmentUpgradeNovaBTCOnly []byte

var developmentUpgraders = map[common.Product][]byte{
	common.ProductBitBox02Multi:       developmentUpgradeMulti,
	common.ProductBitBox02BTCOnly:     developmentUpgradeBTCOnly,
	common.ProductBitBox02PlusMulti:   developmentUpgradeNovaMulti,
	common.ProductBitBox02PlusBTCOnly: developmentUpgradeNovaBTCOnly,
}

func developmentUpgrader(product common.Product) ([]byte, error) {
	compressed, ok := developmentUpgraders[product]
	if !ok {
		return nil, errp.New("unrecognized product")
	}
	return decompressFirmware(compressed)
}

func (device *Device) developmentBootloaderCurrent(info *bootloader.VersionInfo) bool {
	return device.bootloaderVersion.AtLeast(semver.NewSemVer(1, 2, 4)) &&
		info.Stage0Version != nil && *info.Stage0Version >= 3
}

func (device *Device) initDevelopment() error {
	info, err := device.Versions()
	if err != nil {
		return err
	}
	if !device.developmentBootloaderCurrent(info) {
		return nil
	}
	upgrader, err := developmentUpgrader(device.Product())
	if err != nil {
		return err
	}
	pending, err := device.ContainsFirmware(upgrader)
	if err != nil {
		return err
	}
	if pending {
		go func() {
			if err := device.UpgradeFirmware(); err != nil {
				device.log.WithError(err).Error("development upgrade continuation failed")
			}
		}()
	}
	return nil
}

func (device *Device) developmentTarget() (*bootloader.VersionInfo, *firmwareInfo, []byte, error) {
	info, err := device.Versions()
	if err != nil {
		return nil, nil, nil, err
	}
	latest, err := bundledFirmware(device.Product())
	if err != nil {
		return nil, nil, nil, err
	}
	signed, err := latest.signedBinary()
	if err != nil {
		return nil, nil, nil, err
	}
	_, sigdata, _, err := bootloader.ParseSignedFirmware(signed)
	if err != nil {
		return nil, nil, nil, err
	}
	if info.FirmwareVersion > latest.monotonicVersion ||
		info.SigningPubkeysVersion > binary.LittleEndian.Uint32(sigdata[:4]) {
		return nil, nil, nil, errp.New("device version counters exceed the bundled firmware; downgrade is not supported")
	}
	return info, latest, signed, nil
}

func (device *Device) upgradeDevelopment() error {
	info, _, signed, err := device.developmentTarget()
	if err != nil {
		return err
	}
	current := device.developmentBootloaderCurrent(info)
	erased, err := device.Erased()
	if err != nil {
		return err
	}
	device.operationLock.Lock()
	device.operationErased = erased
	device.operationAdditionalUpgradeFollows = !current
	device.operationLock.Unlock()
	if current {
		return device.Device.UpgradeFirmware(signed)
	}
	upgrader, err := developmentUpgrader(device.Product())
	if err != nil {
		return err
	}
	pending, err := device.ContainsFirmware(upgrader)
	if err != nil {
		return err
	}
	if pending {
		return device.Reboot()
	}
	return device.UpgradeUnsignedFirmware(upgrader)
}

func (device *Device) developmentInfo() (*Info, error) {
	info, latest, signed, err := device.developmentTarget()
	if err != nil {
		return nil, err
	}
	erased, err := device.Erased()
	if err != nil {
		return nil, err
	}
	_, sigdata, firmware, err := bootloader.ParseSignedFirmware(signed)
	if err != nil {
		return nil, err
	}
	matches, err := device.ContainsFirmware(firmware)
	if err != nil {
		return nil, err
	}
	return &Info{
		Erased: erased,
		CanUpgrade: !device.developmentBootloaderCurrent(info) ||
			info.FirmwareVersion != latest.monotonicVersion ||
			info.SigningPubkeysVersion != binary.LittleEndian.Uint32(sigdata[:4]) || !matches,
	}, nil
}

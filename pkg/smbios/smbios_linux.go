// Copyright 2016-2021 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package smbios

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

var systabPath = "/sys/firmware/efi/systab"

// SMBIOSBaseEFI finds the SMBIOS entry point address in the EFI System Table.
func SMBIOSBaseEFI() (base int64, size int64, err error) {
	file, err := os.Open(systabPath)
	if err != nil {
		fmt.Printf("SMBIOSBaseEFI: Failed to open /sys/firmware/efi/systab\n")
		return 0, 0, err
	}
	defer file.Close()

	const (
		smbios3 = "SMBIOS3="
		smbios  = "SMBIOS="
	)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		start := ""
		size := int64(0)
		if strings.HasPrefix(line, smbios3) {
			start = strings.TrimPrefix(line, smbios3)
			size = smbios3HeaderSize
			fmt.Printf("SMBIOSBaseEFI: TYPE (%s) found, start:(%s)\n", smbios3, start)
		}
		if strings.HasPrefix(line, smbios) {
			start = strings.TrimPrefix(line, smbios)
			size = smbios2HeaderSize
			fmt.Printf("SMBIOSBaseEFI: TYPE (%s) found, start:(%s)\n", smbios, start)
		}
		if start == "" {
			continue
		}
		base, err := strconv.ParseInt(start, 0, 63)
		if err != nil {
			fmt.Printf("SMBIOSBaseEFI: failed to parse base string (%s) with error:%v\n", start, err)
			continue
		}
		return base, size, nil
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error while reading EFI systab: %v", err)
	}
	return 0, 0, fmt.Errorf("invalid /sys/firmware/efi/systab file")
}

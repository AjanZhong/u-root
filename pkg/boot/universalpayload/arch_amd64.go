// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package universalpayload

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var sysfsCPUInfoPath = "/proc/cpuinfo"
var (
	ErrCPUAddressConvert  = errors.New("failed to convert physical bits size")
	ErrCPUAddressRead     = errors.New("failed to read 'address sizes'")
	ErrCPUAddressNotFound = errors.New("'address sizes' information not found")
)

// Get Physical Address size from sysfs node /proc/cpuinfo.
// Both Physical and Virtual Address size will be prompted as format:
// "address sizes	: 39 bits physical, 48 bits virtual"
// Use regular expression to fetch the integer of Physical Address
// size before "bits physical" keyword
func getPhysicalAddressSizes() (uint8, error) {
	file, err := os.Open(sysfsCPUInfoPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", sysfsCPUInfoPath, err)
	}
	defer file.Close()

	// Regular expression to match the address size line
	re := regexp.MustCompile(`address sizes\s*:\s*(\d+)\s+bits physical,\s*(\d+)\s+bits virtual`)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if match := re.FindStringSubmatch(line); match != nil {
			// Convert the physical bits size to integer
			physicalBits, err := strconv.ParseUint(match[1], 10, 8)
			if err != nil {
				return 0, errors.Join(ErrCPUAddressConvert, err)
			}
			return uint8(physicalBits), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("%w: file: %s, err: %w", ErrCPUAddressRead, sysfsCPUInfoPath, err)
	}

	return 0, ErrCPUAddressNotFound
}

// Constrcut trampoline code before jump to entry point of FIT image.
// Due to lack of support to set value of General Purpose Registers in kexec,
// bootloader parameter needs to be prepared in trampoline code.
// Also stack is prepared in trampoline code snippet to ensure no data leak.
//
// Trampoline code snippet is prepared as following:
//
//	trampoline[0 - 6]   : mov rax, qword ptr [rip+0x19]
//	trampoline[7 - 9]   : mov rsp, rax
//	trampoline[10 - 16] : mov rax, qword ptr [rip+0x17]
//	trampoline[17 - 19] : mov rcx, rax
//	trampoline[20 - 26] : mov rax, qword ptr [rip+0x15]
//	trampoline[27 - 28] : jmp rax
//	trampoline[29 - 31] : padding for alignment
//	trampoline[32 - 39] : Top of stack address
//	trampoline[40 - 47] : Base address of bootloader parameter
//	trampoline[48 - 55] : Entry point of FIT image
func constructTrampoline(buf []uint8, hobAddr uint64, entry uint64) []uint8 {
	loadStackAddress := []uint8{0x48, 0x8b, 0x05, 0x19, 0x00, 0x00, 0x00}
	setStackAddress := []uint8{0x48, 0x89, 0xc4}
	loadBootparameter := []uint8{0x48, 0x8b, 0x05, 0x17, 0x00, 0x00, 0x00}
	setBootparameter := []uint8{0x48, 0x89, 0xc1}
	loadKernelAddress := []uint8{0x48, 0x8b, 0x05, 0x15, 0x00, 0x00, 0x00}
	jumpToKernelAddress := []uint8{0xff, 0xe0}
	padForAlignment := []uint8{0x00, 0x00, 0x00}

	buf = append(buf, loadStackAddress...)
	buf = append(buf, setStackAddress...)
	buf = append(buf, loadBootparameter...)
	buf = append(buf, setBootparameter...)
	buf = append(buf, loadKernelAddress...)
	buf = append(buf, jumpToKernelAddress...)
	buf = append(buf, padForAlignment...)

	stackTop := hobAddr + tmpStackTop
	appendUint64 := func(slice []uint8, value uint64) []uint8 {
		tmpBytes := make([]uint8, 8)
		binary.LittleEndian.PutUint64(tmpBytes, value)
		return append(slice, tmpBytes...)
	}

	buf = appendUint64(buf, stackTop)
	buf = appendUint64(buf, hobAddr)
	buf = appendUint64(buf, entry)

	return buf
}

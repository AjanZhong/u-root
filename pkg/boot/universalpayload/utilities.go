// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package universalpayload

import (
	"bufio"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"

	"github.com/u-root/u-root/pkg/dt"
)

// Properties to be fetched from device tree.
const (
	FirstLevelNodeName     = "images"
	SecondLevelNodeName    = "tianocore"
	LoadAddrPropertyName   = "load"
	EntryAddrPropertyName  = "entry-start"
	DataOffsetPropertyName = "data-offset"
	DataSizePropertyName   = "data-size"
)

const (
	tmpHobSize     = 0x1000
	tmpStackSize   = 0x1000
	tmpStackTop    = 0x2000
	tmpEntryOffset = 0x2000
	trampolineSize = 0x1000
)

const (
	// Relocation Types
	IMAGE_REL_BASED_ABSOLUTE = 0
	IMAGE_REL_BASED_HIGHLOW  = 3
	IMAGE_REL_BASED_DIR64    = 10
)

var sysfsCPUInfoPath = "/proc/cpuinfo"

type FdtLoad struct {
	Load       uint64
	EntryStart uint64
	DataOffset uint32
	DataSize   uint32
}

// Errors returned by utilities
var (
	ErrFailToReadFdtFile       = errors.New("failed to read fdt file")
	ErrNodeImagesNotFound      = fmt.Errorf("failed to find '%s' node", FirstLevelNodeName)
	ErrNodeTianocoreNotFound   = fmt.Errorf("failed to find '%s' node", SecondLevelNodeName)
	ErrNodeLoadNotFound        = fmt.Errorf("failed to find get '%s' property", LoadAddrPropertyName)
	ErrNodeEntryStartNotFound  = fmt.Errorf("failed to find get '%s' property", EntryAddrPropertyName)
	ErrNodeDataOffsetNotFound  = fmt.Errorf("failed to find get '%s' property", DataOffsetPropertyName)
	ErrNodeDataSizeNotFound    = fmt.Errorf("failed to find get '%s' property", DataSizePropertyName)
	ErrFailToConvertLoad       = fmt.Errorf("failed to convert property '%s' to u64", LoadAddrPropertyName)
	ErrFailToConvertEntryStart = fmt.Errorf("failed to convert property '%s' to u64", EntryAddrPropertyName)
	ErrFailToConvertDataOffset = fmt.Errorf("failed to convert property '%s' to u32", DataOffsetPropertyName)
	ErrFailToConvertDataSize   = fmt.Errorf("failed to convert property '%s' to u32", DataSizePropertyName)
	ErrPeFailToGetPageRVA      = fmt.Errorf("failed to read pagerva during pe file relocation")
	ErrPeFailToGetBlockSize    = fmt.Errorf("failed to read block size during pe file relocation")
	ErrPeFailToGetEntry        = fmt.Errorf("failed to get entry during pe file relocation")
	ErrPeFailToCreatePeFile    = fmt.Errorf("failed to create pe file")
	ErrPeFailToGetRelocData    = fmt.Errorf("failed to get .reloc section data")
	ErrPeUnsupportedPeHeader   = fmt.Errorf("unsupported pe header format")
	ErrPeRelocOutOfBound       = fmt.Errorf("relocation address out of bounds during pe file relocation")
	ErrCPUAddressNotFound      = errors.New("'address sizes' information not found")
	ErrCPUAddressRead          = errors.New("failed to read 'address sizes'")
	ErrCPUAddressConvert       = errors.New("failed to convert physical bits size")
	ErrAlignPadRange           = errors.New("failed to align pad size, out of range")
)

// GetFdtInfo Device Tree Blob resides at the start of FIT binary. In order to
// get the expected load and entry point address, need to walk through
// DTB to get value of properties 'load' and 'entry-start'.
//
// The simplified device tree layout is:
//
//	/{
//	    images {
//	        tianocore {
//	            data-offset = <0x00001000>;
//	            data-size = <0x00010000>;
//	            entry-start = <0x00000000 0x00805ac3>;
//	            load = <0x00000000 0x00800000>;
//	        }
//	    }
//	 }
func GetFdtInfo(name string) (*FdtLoad, error) {
	return getFdtInfo(name, nil)
}

func getFdtInfo(name string, dtb io.ReaderAt) (*FdtLoad, error) {
	var fdt *dt.FDT
	var err error

	if dtb != nil {
		fdt, err = dt.ReadFDT(io.NewSectionReader(dtb, 0, math.MaxInt64))
	} else {
		fdt, err = dt.ReadFile(name)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: fdt file: %s, err: %w", ErrFailToReadFdtFile, name, err)
	}

	firstLevelNode, succeed := fdt.NodeByName(FirstLevelNodeName)
	if succeed != true {
		return nil, ErrNodeImagesNotFound
	}

	secondLevelNode, succeed := firstLevelNode.NodeByName(SecondLevelNodeName)
	if succeed != true {
		return nil, ErrNodeTianocoreNotFound
	}

	loadAddrProp, succeed := secondLevelNode.LookProperty(LoadAddrPropertyName)
	if succeed != true {
		return nil, ErrNodeLoadNotFound
	}

	loadAddr, err := loadAddrProp.AsU64()
	if err != nil {
		return nil, errors.Join(ErrFailToConvertLoad, err)
	}

	entryAddrProp, succeed := secondLevelNode.LookProperty(EntryAddrPropertyName)
	if succeed != true {
		return nil, ErrNodeEntryStartNotFound
	}

	entryAddr, err := entryAddrProp.AsU64()
	if err != nil {
		return nil, errors.Join(ErrFailToConvertEntryStart, err)
	}

	dataOffsetProp, succeed := secondLevelNode.LookProperty(DataOffsetPropertyName)
	if succeed != true {
		return nil, ErrNodeDataOffsetNotFound
	}

	dataOffset, err := dataOffsetProp.AsU32()
	if err != nil {
		return nil, errors.Join(ErrFailToConvertDataOffset, err)
	}

	dataSizeProp, succeed := secondLevelNode.LookProperty(DataSizePropertyName)
	if succeed != true {
		return nil, ErrNodeDataSizeNotFound
	}

	dataSize, err := dataSizeProp.AsU32()
	if err != nil {
		return nil, errors.Join(ErrFailToConvertDataSize, err)
	}

	return &FdtLoad{
		Load:       loadAddr,
		EntryStart: entryAddr,
		DataOffset: dataOffset,
		DataSize:   dataSize,
	}, nil
}

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

// alignHOBLength writes pad bytes at the end of a HOB buf
// It's because we calculate HOB length with golang, while write bytes to the buf with actual length
func alignHOBLength(expectLen uint64, bufLen int, buf *bytes.Buffer) error {
	if expectLen < uint64(bufLen) {
		return ErrAlignPadRange
	}

	if expectLen > math.MaxInt {
		return ErrAlignPadRange
	}
	if padLen := int(expectLen) - bufLen; padLen > 0 {
		pad := make([]byte, padLen)
		if err := binary.Write(buf, binary.LittleEndian, pad); err != nil {
			return err
		}
	}
	return nil
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

// Walk through .reloc section, update expected address to actual address
// which is calculated with recloation offset. Currently, only type of
// IMAGE_REL_BASED_DIR64(10) found in .reloc setcion, update this type
// of address only.
func relocatePE(relocData []byte, delta uint64, data []byte) error {
	r := bytes.NewReader(relocData)

	for {
		// Read relocation block header
		var pageRVA uint32
		var blockSize uint32

		err := binary.Read(r, binary.LittleEndian, &pageRVA)
		if err == io.EOF {
			break // End of relocations
		}
		if err != nil {
			return ErrPeFailToGetPageRVA
		}
		err = binary.Read(r, binary.LittleEndian, &blockSize)
		if err != nil {
			return ErrPeFailToGetBlockSize
		}

		// Block size includes the header, so the number of entries is (blockSize - 8) / 2
		entryCount := (blockSize - 8) / 2
		for i := 0; i < int(entryCount); i++ {
			var entry uint16
			err := binary.Read(r, binary.LittleEndian, &entry)
			if err != nil {
				return ErrPeFailToGetEntry
			}

			// Type is in the high 4 bits, offset is in the low 12 bits
			entryType := entry >> 12
			entryOffset := entry & 0xfff

			// Only type IMAGE_REL_BASED_DIR64(10) found
			if entryType == IMAGE_REL_BASED_DIR64 {
				// Perform relocation
				relocAddr := pageRVA + uint32(entryOffset)
				if relocAddr >= uint32(len(data)) {
					return ErrPeRelocOutOfBound
				}
				originalValue := binary.LittleEndian.Uint64(data[relocAddr:])
				relocatedValue := originalValue + delta
				binary.LittleEndian.PutUint64(data[relocAddr:], relocatedValue)
			}
		}
	}
	return nil
}

func relocateFdtdata(dst uint64, fdtLoad *FdtLoad, data []byte) error {
	// Get the region of universalpayload binary from FIT image
	start := fdtLoad.DataOffset
	end := fdtLoad.DataOffset + fdtLoad.DataSize

	reader := bytes.NewReader(data[start:end])

	peFile, err := pe.NewFile(reader)
	if err != nil {
		return ErrPeFailToCreatePeFile
	}
	defer peFile.Close()

	optionalHeader, success := peFile.OptionalHeader.(*pe.OptionalHeader64)
	if !success {
		return ErrPeUnsupportedPeHeader
	}

	preBase := optionalHeader.ImageBase
	delta := dst + uint64(fdtLoad.DataOffset) - preBase

	for _, section := range peFile.Sections {
		if section.Name == ".reloc" {
			relocData, err := section.Data()
			if err != nil {
				return ErrPeFailToGetRelocData
			}

			if err := relocatePE(relocData, delta, data[start:end]); err != nil {
				return err
			}
		}
	}

	fdtLoad.EntryStart = dst + (fdtLoad.EntryStart - fdtLoad.Load)
	fdtLoad.Load = dst

	return nil
}

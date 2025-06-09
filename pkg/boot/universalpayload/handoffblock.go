// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package universalpayload

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	guid "github.com/google/uuid"
	"github.com/u-root/u-root/pkg/align"
	"github.com/u-root/u-root/pkg/boot/kexec"
)

// EFIHOBGenericHeader types
type EFIHOBType uint16
type EFIHOBLength uint16
type EFIHOBReserved uint32

// EFIHOBGenericHeader describes the format and size of the data inside the HOB
type EFIHOBGenericHeader struct {
	HOBType   EFIHOBType
	HOBLength EFIHOBLength
	Reserved  EFIHOBReserved
}

// EFIHOBType values
const (
	EFIHOBTypeHandoff            EFIHOBType = 0x0001
	EFIHOBTypeMemoryAllocation   EFIHOBType = 0x0002
	EFIHOBTypeResourceDescriptor EFIHOBType = 0x0003
	EFIHOBTypeGUIDExtension      EFIHOBType = 0x0004
	EFIHOBTypeFv                 EFIHOBType = 0x0005
	EFIHOBTypeCPU                EFIHOBType = 0x0006
	EFIHOBTypeMemoryPool         EFIHOBType = 0x0007
	EFIHOBTypeFv2                EFIHOBType = 0x0009
	EFIHOBTypeLoadPeimUnused     EFIHOBType = 0x000A
	EFIHOBTypeUEFICapsule        EFIHOBType = 0x000B
	EFIHOBTypeFv3                EFIHOBType = 0x000C
	EFIHOBTypeUnused             EFIHOBType = 0xFFFE
	EFIHOBTypeEndOfHOBList       EFIHOBType = 0xFFFF
)

type EFIResourceType uint32

// EFIResourceType values
const (
	EFIResourceSystemMemory       EFIResourceType = 0x00000000
	EFIResourceMemoryMappedIO     EFIResourceType = 0x00000001
	EFIResourceIO                 EFIResourceType = 0x00000002
	EFIResourceEFIFirmwareDevice  EFIResourceType = 0x00000003
	EFIResourceMemoryMappedIOPort EFIResourceType = 0x00000004
	EFIResourceMemoryReserved     EFIResourceType = 0x00000005
	EFIResourceIOReserved         EFIResourceType = 0x00000006
	EFIResourceMemoryUnaccepted   EFIResourceType = 0x00000007
	EFIResourceMaxMemoryType      EFIResourceType = 0x00000008
	EFIResourceUnimplemented      EFIResourceType = 0x00000008
)

type EFIResourceAttributeType uint32

// EFIResourceAttributeType values
const (
	EFIResourceAttributePresent               EFIResourceAttributeType = 0x00000001
	EFIResourceAttributeInitialized           EFIResourceAttributeType = 0x00000002
	EFIResourceAttributeTested                EFIResourceAttributeType = 0x00000004
	EFIResourceAttributeReadProtected         EFIResourceAttributeType = 0x00000080
	EFIResourceAttributeWriteProtected        EFIResourceAttributeType = 0x00000100
	EFIResourceAttributeExecutionProtected    EFIResourceAttributeType = 0x00000200
	EFIResourceAttributePersistent            EFIResourceAttributeType = 0x00800000
	EFIResourceAttributeSingleBitECC          EFIResourceAttributeType = 0x00000008
	EFIResourceAttributeMultipleBitECC        EFIResourceAttributeType = 0x00000010
	EFIResourceAttributeECCReserved1          EFIResourceAttributeType = 0x00000020
	EFIResourceAttributeECCReserved2          EFIResourceAttributeType = 0x00000040
	EFIResourceAttributeUncacheable           EFIResourceAttributeType = 0x00000400
	EFIResourceAttributeWriteCombineable      EFIResourceAttributeType = 0x00000800
	EFIResourceAttributeWriteThroughCacheable EFIResourceAttributeType = 0x00001000
	EFIResourceAttributeWriteBackCacheable    EFIResourceAttributeType = 0x00002000
	EFIResourceAttribute16BitIO               EFIResourceAttributeType = 0x00004000
	EFIResourceAttribute32BitIO               EFIResourceAttributeType = 0x00008000
	EFIResourceAttribute64BitIO               EFIResourceAttributeType = 0x00010000
	EFIResourceAttributeUncachedExported      EFIResourceAttributeType = 0x00020000
	EFIResourceAttributeReadProtectable       EFIResourceAttributeType = 0x00100000
	EFIResourceAttributeWriteProtectable      EFIResourceAttributeType = 0x00200000
	EFIResourceAttributeExecutionProtectable  EFIResourceAttributeType = 0x00400000
	EFIResourceAttributePersistable           EFIResourceAttributeType = 0x01000000
	EFIResourceAttributeReadOnlyProtected     EFIResourceAttributeType = 0x00040000
	EFIResourceAttributeReadOnlyProtectable   EFIResourceAttributeType = 0x00080000
	EFIResourceAttributeEncrypted             EFIResourceAttributeType = 0x04000000
	EFIResourceAttributeSpecialPurpose        EFIResourceAttributeType = 0x08000000
	EFIResourceAttributeMoreReliable          EFIResourceAttributeType = 0x02000000
)

type EFIPhysicalAddress uint64
type EFIHOBHandOffBootModeType uint32
type EFIHOBHandoffInfoVersionType uint32

// EFIHOBHandoffInfoTable values
const (
	EFIHOBHandoffInfoVersion          EFIHOBHandoffInfoVersionType = 0x09
	EFIHOBHandoffInfoBootMode         EFIHOBHandOffBootModeType    = 0x0
	EFIHOBHandoffInfoEFIMemoryTop     EFIPhysicalAddress           = 0x4E00000
	EFIHOBHandoffInfoEFIMemoryBottom  EFIPhysicalAddress           = 0x800000
	EFIHOBHandoffInfoFreeEFIMemoryTop EFIPhysicalAddress           = 0x4E00000
	EFIHOBHandoffInfoFreeMemoryBottom EFIPhysicalAddress           = 0xE00000
)

// EFIHOBHandoffInfoTable appears in the first in HOB list, which contains some general information
type EFIHOBHandoffInfoTable struct {
	Header           EFIHOBGenericHeader
	Version          EFIHOBHandoffInfoVersionType
	BootMode         EFIHOBHandOffBootModeType
	MemoryTop        EFIPhysicalAddress
	MemoryBottom     EFIPhysicalAddress
	FreeMemoryTop    EFIPhysicalAddress
	FreeMemoryBottom EFIPhysicalAddress
	EndOfHOBList     EFIPhysicalAddress
}

// EFIHOBResourceDescriptor describes all resource properties found on the processor host bus
type EFIHOBResourceDescriptor struct {
	Header            EFIHOBGenericHeader
	Owner             guid.UUID // EFI GUID (assuming a byte array for simplicity)
	ResourceType      EFIResourceType
	ResourceAttribute EFIResourceAttributeType
	PhysicalStart     EFIPhysicalAddress
	ResourceLength    uint64
}

// EFIHOBFirmwareVolume describes the location of firmware locations
type EFIHOBFirmwareVolume struct {
	Header      EFIHOBGenericHeader
	BaseAddress EFIPhysicalAddress
	Length      uint64
}

// EFIHOBGUIDType describes the extension information which allows
// writing executable content in the HOB producer phase.
type EFIHOBGUIDType struct {
	Header EFIHOBGenericHeader
	Name   guid.UUID
}

// EFIHOBCPU describes CPU information
type EFIHOBCPU struct {
	Header            EFIHOBGenericHeader
	SizeOfMemorySpace uint8
	SizeOfIOSpace     uint8
	Reserved          [6]byte
}

// Common Constants
// According to Intel SDM Volume 1 Chapter 19.3 "I/O ADDRESS SPACE":
// The I/O address space consists of 2^16 (64K) individually addressable
// 8-bit I/O ports, numbered 0 through FFFFH.
// Set the default IO Address size to 16
const (
	DefaultIOAddressSize uint8 = 16
)

// MemoryRegion represents a memory region with start and end addresses
type MemoryRegion struct {
	Start uint64
	End   uint64
}

// Global arrays to store memory regions
var runtimeCodeRegions []MemoryRegion
var runtimeDataRegions []MemoryRegion

// parseAndMergeMemoryRegions parses dmesg log and merges contiguous memory regions
func parseAndMergeMemoryRegions(dmesgLog string) {
	scanner := bufio.NewScanner(strings.NewReader(dmesgLog))

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "efi: memattr:") {
			continue
		}

		// Extract memory range
		parts := strings.Split(line, "[")
		if len(parts) != 2 {
			continue
		}

		// Parse memory range, handling the optional "!" mark
		rangeStr := strings.TrimSpace(strings.Split(parts[0], "efi: memattr:")[1])
		// Remove the "!" mark if present
		rangeStr = strings.TrimSpace(strings.TrimPrefix(rangeStr, "!"))
		rangeParts := strings.Split(rangeStr, "-")
		if len(rangeParts) != 2 {
			continue
		}

		start, err := strconv.ParseUint(strings.TrimPrefix(rangeParts[0], "0x"), 16, 64)
		if err != nil {
			continue
		}

		end, err := strconv.ParseUint(strings.TrimPrefix(rangeParts[1], "0x"), 16, 64)
		if err != nil {
			continue
		}

		// Determine if this is Runtime Code or Runtime Data
		isRuntimeCode := strings.Contains(parts[1], "Runtime Code")
		targetRegions := &runtimeDataRegions
		if isRuntimeCode {
			targetRegions = &runtimeCodeRegions
		}

		// Check if this region can be merged with existing regions
		merged := false
		for i := range *targetRegions {
			// If new region starts right after an existing region ends
			if start == (*targetRegions)[i].End+1 {
				(*targetRegions)[i].End = end
				merged = true
				break
			}
			// If new region ends right before an existing region starts
			if end+1 == (*targetRegions)[i].Start {
				(*targetRegions)[i].Start = start
				merged = true
				break
			}
		}

		// If not merged, append as new region
		if !merged {
			*targetRegions = append(*targetRegions, MemoryRegion{
				Start: start,
				End:   end,
			})
		}
	}
}

type EFIMemoryMapHOB []EFIHOBResourceDescriptor

// readDmesgContent attempts to read dmesg content from either /var/log/dmesg or /proc/kmsg
func readDmesgContent() ([]byte, error) {
	// Try to read from /var/log/dmesg first
	dmesgContent, err := os.ReadFile("/var/log/dmesg")
	if err == nil {
		return dmesgContent, nil
	}

	// If /var/log/dmesg is not available, try /proc/kmsg
	kmsgFile, err := os.Open("/proc/kmsg")
	if err != nil {
		return nil, fmt.Errorf("failed to read dmesg: %v", err)
	}
	defer kmsgFile.Close()

	scanner := bufio.NewScanner(kmsgFile)
	var content strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		content.WriteString(line)
		content.WriteString("\n")
		if strings.Contains(line, "smp: Bringing up secondary CPUs") {
			break
		}
	}
	return []byte(content.String()), nil
}

// isInRuntimeMemoryRegion checks if the given address is within any of the runtime code regions
func isInRuntimeMemoryRegion(addr uint64) bool {
	for _, region := range runtimeCodeRegions {
		if addr >= region.Start && addr <= region.End {
			return true
		}
	}
	return false
}

// Translate System Map with "System RAM" type to Resource code HOBs.
func hobFromMemMap(memMap kexec.MemoryMap) (EFIMemoryMapHOB, uint64) {
	var memMapHOB EFIMemoryMapHOB
	var length uint64
	var resourceType EFIResourceType

	// Try to read dmesg content
	dmesgContent, err := readDmesgContent()
	if err != nil {
		warningMsg = append(warningMsg, err)
	}

	// Parse and merge memory regions from dmesg if content was read successfully
	if len(dmesgContent) > 0 {
		parseAndMergeMemoryRegions(string(dmesgContent))
	}

	fmt.Printf("Runtime Code Regions:\n")
	for _, region := range runtimeCodeRegions {
		fmt.Printf("  Start: 0x%x, End: 0x%x\n", region.Start, region.End)
	}
	fmt.Printf("Runtime Data Regions:\n")
	for _, region := range runtimeDataRegions {
		fmt.Printf("  Start: 0x%x, End: 0x%x\n", region.Start, region.End)
	}

	for _, entry := range memMap {
		memType := strings.TrimSpace(string(entry.Type))

		// Skip resource region of PCI Bus. UniversalPayload utilizes its own
		// PciHostBridgeDxe to enumerate all Root Bridges in PCI Bus.
		if strings.Contains(memType, "PCI Bus") {
			continue
		}

		if EFIPhysicalAddress(entry.Start) == 0xFEE0_0000 {
			continue
		}

		if memType == kexec.RangeRAM.String() {
			resourceType = EFIResourceSystemMemory
		} else if memType == kexec.RangeReserved.String() {
			if isInRuntimeMemoryRegion(uint64(entry.Start)) {
				continue
			}
			resourceType = EFIResourceMemoryReserved
		} else {
			// Treat all other types to be mapped device MMIO address
			resourceType = EFIResourceMemoryMappedIO
		}

		memMapHOB = append(memMapHOB, EFIHOBResourceDescriptor{
			Header: EFIHOBGenericHeader{
				HOBType:   EFIHOBTypeResourceDescriptor,
				HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBResourceDescriptor{})),
			},
			ResourceType: resourceType,
			ResourceAttribute: EFIResourceAttributePresent |
				EFIResourceAttributeInitialized |
				EFIResourceAttributeTested |
				EFIResourceAttributeUncacheable |
				EFIResourceAttributeWriteCombineable |
				EFIResourceAttributeWriteThroughCacheable |
				EFIResourceAttributeWriteBackCacheable,
			PhysicalStart:  EFIPhysicalAddress(entry.Start),
			ResourceLength: uint64(align.UpPage(entry.Size)),
		})
		length += uint64(unsafe.Sizeof(EFIHOBResourceDescriptor{}))
	}

	for _, entry := range runtimeCodeRegions {
		size := entry.End - entry.Start + 1

		memMapHOB = append(memMapHOB, EFIHOBResourceDescriptor{
			Header: EFIHOBGenericHeader{
				HOBType:   EFIHOBTypeResourceDescriptor,
				HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBResourceDescriptor{})),
			},
			ResourceType: EFIResourceSystemMemory,
			ResourceAttribute: EFIResourceAttributePresent |
				EFIResourceAttributeInitialized |
				EFIResourceAttributeTested |
				EFIResourceAttributeUncacheable |
				EFIResourceAttributeWriteCombineable |
				EFIResourceAttributeWriteThroughCacheable |
				EFIResourceAttributeWriteBackCacheable,
			PhysicalStart:  EFIPhysicalAddress(entry.Start),
			ResourceLength: uint64(align.UpPage(size)),
		})
		length += uint64(unsafe.Sizeof(EFIHOBResourceDescriptor{}))
	}

	for _, entry := range runtimeDataRegions {
		size := entry.End - entry.Start + 1

		memMapHOB = append(memMapHOB, EFIHOBResourceDescriptor{
			Header: EFIHOBGenericHeader{
				HOBType:   EFIHOBTypeResourceDescriptor,
				HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBResourceDescriptor{})),
			},
			ResourceType: EFIResourceMemoryReserved,
			ResourceAttribute: EFIResourceAttributePresent |
				EFIResourceAttributeInitialized |
				EFIResourceAttributeTested |
				EFIResourceAttributeUncacheable |
				EFIResourceAttributeWriteCombineable |
				EFIResourceAttributeWriteThroughCacheable |
				EFIResourceAttributeWriteBackCacheable,
			PhysicalStart:  EFIPhysicalAddress(entry.Start),
			ResourceLength: uint64(align.UpPage(size)),
		})
		length += uint64(unsafe.Sizeof(EFIHOBResourceDescriptor{}))
	}

	// Do not copy this snippet of code, this is for testing only. -- start
	// If you want to test this, please replace 0x4000000 with
	// value of gEfiMdeModulePkgTokenSpaceGuid.PcdFlashNvStorageVariableBase64
	memMapHOB = append(memMapHOB, EFIHOBResourceDescriptor{
		Header: EFIHOBGenericHeader{
			HOBType:   EFIHOBTypeResourceDescriptor,
			HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBResourceDescriptor{})),
		},
		ResourceType: EFIResourceMemoryReserved,
		ResourceAttribute: EFIResourceAttributePresent |
			EFIResourceAttributeInitialized |
			EFIResourceAttributeTested |
			EFIResourceAttributeUncacheable |
			EFIResourceAttributeWriteCombineable |
			EFIResourceAttributeWriteThroughCacheable |
			EFIResourceAttributeWriteBackCacheable,
		PhysicalStart:  EFIPhysicalAddress(0x4000000),
		ResourceLength: uint64(0x100000),
	})
	length += uint64(unsafe.Sizeof(EFIHOBResourceDescriptor{}))
	// Do not copy this snippet of code, this is for testing only. -- End

	length += appendAddonMemMap(&memMapHOB)

	return memMapHOB, length
}

func hobCreateEndHOB() EFIHOBGenericHeader {
	return EFIHOBGenericHeader{
		HOBType:   EFIHOBTypeEndOfHOBList,
		HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBGenericHeader{})),
		Reserved:  0,
	}
}

// Create handoff information HOB with specified length.
// Handoff info HOB should be created after all HOBs in HOB list
// have been created, since length of HOB list should be provided
// as input parameter.
func hobCreateEFIHOBHandoffInfoTable(length uint64) EFIHOBHandoffInfoTable {
	length += uint64(unsafe.Sizeof(EFIHOBHandoffInfoTable{}))

	return EFIHOBHandoffInfoTable{
		Header: EFIHOBGenericHeader{
			HOBType:   EFIHOBTypeHandoff,
			HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBHandoffInfoTable{})),
		},
		Version:          EFIHOBHandoffInfoVersion,
		BootMode:         EFIHOBHandoffInfoBootMode,
		MemoryTop:        EFIHOBHandoffInfoEFIMemoryTop,
		MemoryBottom:     EFIHOBHandoffInfoEFIMemoryBottom,
		FreeMemoryTop:    EFIHOBHandoffInfoFreeEFIMemoryTop,
		FreeMemoryBottom: EFIHOBHandoffInfoFreeMemoryBottom + EFIPhysicalAddress(length+8),
		EndOfHOBList:     EFIHOBHandoffInfoFreeMemoryBottom + EFIPhysicalAddress(length),
	}
}

func hobCreateEFIHOBCPU() (*EFIHOBCPU, error) {
	phyAddrSize, err := getPhysicalAddressSizes()
	if err != nil {
		return nil, err
	}

	return &EFIHOBCPU{
		Header: EFIHOBGenericHeader{
			HOBType:   EFIHOBTypeCPU,
			HOBLength: EFIHOBLength(unsafe.Sizeof(EFIHOBCPU{})),
		},
		SizeOfMemorySpace: phyAddrSize,
		SizeOfIOSpace:     DefaultIOAddressSize,
	}, nil
}

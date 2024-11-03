// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package universalpayload

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"unsafe"

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

type FdtLoad struct {
	Load       uint64
	EntryStart uint64
	DataOffset uint32
	DataSize   uint32
}

const sysfsFbPath = "/dev/fb0"

type FbBitfield struct {
	Offset   uint32 // beginning of bitfield
	Length   uint32 // length of bitfield
	MsbRight uint32 // != 0 : Most significant bit is right
}

// Definitions for ioctl and framebuffer structures in Go
const (
	FbIotclVscreeninfo    = 0x4600
	FbIoctlGetFscreenInfo = 0x4602
)

type FbVarScreenInfo struct {
	Xres         uint32
	Yres         uint32
	XresVirtual  uint32
	YresVirtual  uint32
	Xoffset      uint32
	Yoffset      uint32
	BitsPerPixel uint32
	Grayscale    uint32
	Red          FbBitfield
	Green        FbBitfield
	Blue         FbBitfield
	Transp       FbBitfield
	Nonstd       uint32
	Activate     uint32
	Height       uint32
	Width        uint32
	AccelFlags   uint32
	PixClock     uint32
	LeftMargin   uint32
	RightMargin  uint32
	UpperMargin  uint32
	LowerMargin  uint32
	HsyncLen     uint32
	VsyncLen     uint32
	Sync         uint32
	Vmode        uint32
	Rotate       uint32
	Colorspace   uint32
	Reserved     [4]uint32
}

type FbFixScreenInfo struct {
	ID           [16]byte
	SmemStart    uint64
	SmemLen      uint32
	Type         uint32
	TypeAux      uint32
	Visual       uint32
	Xpanstep     uint16
	Ypanstep     uint16
	Ywrapstep    uint16
	LineLength   uint32
	MmioStart    uint64
	MmioLen      uint32
	Accel        uint32
	Capabilities uint16
	Reserved     [2]uint16
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
			return fmt.Errorf("%w: err: %w", ErrPeFailToGetPageRVA, err)
		}
		if err = binary.Read(r, binary.LittleEndian, &blockSize); err != nil {
			return fmt.Errorf("%w: err: %w", ErrPeFailToGetBlockSize, err)
		}

		// Block size includes the header, so the number of entries is (blockSize - 8) / 2
		entryCount := (blockSize - 8) / 2
		for i := 0; i < int(entryCount); i++ {
			var entry uint16
			if err := binary.Read(r, binary.LittleEndian, &entry); err != nil {
				return fmt.Errorf("%w: err: %w", ErrPeFailToGetEntry, err)
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

func relocateFdtData(dst uint64, fdtLoad *FdtLoad, data []byte) error {
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

func constructFbScreenInfo() (*EfiPeiGraphicInfoHob, error) {
	// Open the framebuffer device
	fb, err := os.Open(sysfsFbPath)
	if err != nil {
		fmt.Printf("Error opening framebuffer device: %v\n", err)
		return nil, err
	}

	// Get variable screen info
	var vinfo FbVarScreenInfo
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fb.Fd(), FbIotclVscreeninfo, uintptr(unsafe.Pointer(&vinfo)))
	if errno != 0 {
		fmt.Printf("Error getting variable screen info: %v\n", errno)
		return nil, err
	}

	// Get fixed screen info
	var finfo FbFixScreenInfo
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fb.Fd(), FbIoctlGetFscreenInfo, uintptr(unsafe.Pointer(&finfo)))
	if errno != 0 {
		fmt.Printf("Error getting fixed screen info: %v\n", errno)
		return nil, err
	}

	fb.Close()

	format := PixelRedGreenBlueReserved8BitPerColor
	if vinfo.Red.Offset == 16 && vinfo.Green.Offset == 8 && vinfo.Blue.Offset == 0 {
		format = PixelBlueGreenRedReserved8BitPerColor
	}

	return &EfiPeiGraphicInfoHob{
		FrameBufferBase: finfo.SmemStart,
		FrameBufferSize: finfo.SmemLen,
		GraphicsMode: EfiGraphicOutpuModeInfo{
			Version:              0,
			HorizontalResolution: vinfo.Xres,
			VerticalResolution:   vinfo.Yres,
			PixelFormat:          format,
			PixelInformation: EfiPixelBitmask{
				RedMask:      ((1 << vinfo.Red.Length) - 1) << vinfo.Red.Offset,
				GreenMask:    ((1 << vinfo.Green.Length) - 1) << vinfo.Green.Offset,
				BlueMask:     ((1 << vinfo.Blue.Length) - 1) << vinfo.Blue.Offset,
				ReservedMask: ((1 << vinfo.Transp.Length) - 1) << vinfo.Transp.Offset,
			},
			PixelsPerScanLine: finfo.LineLength / ((vinfo.BitsPerPixel + 7) / 8),
		},
	}, nil
}

func constructGfxDevInfo() (*EfiPeiGraphicDeviceInfoHob, error) {

	return &EfiPeiGraphicDeviceInfoHob{
		VendorId:          0x1234,
		DeviceId:          0x1111,
		SubsystemVendorId: 0x1af4,
		SubsystemId:       0x1100,
		RevisionId:        0x0,
		BarIndex:          0x0,
	}, nil
}

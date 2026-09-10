// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	nvmeAdminGetLogPage = 0x02
	nvmeDiscoverLogID   = 0x70
	discHeaderSize      = 1024
	discEntrySize       = 1024
	maxLogXfer          = 4096
	maxDiscEntries      = 1024
	maxDiscRetries      = 5

	// NVME_IOCTL_ADMIN_CMD is _IOWR('N', 0x41, struct nvme_passthru_cmd).
	nvmeIoctlAdminCmd = 0xC0484E41
)

type nvmePassthruCmd struct {
	Opcode      uint8
	Flags       uint8
	Rsvd1       uint16
	NSID        uint32
	CDW2        uint32
	CDW3        uint32
	Metadata    uint64
	Addr        uint64
	MetadataLen uint32
	DataLen     uint32
	CDW10       uint32
	CDW11       uint32
	CDW12       uint32
	CDW13       uint32
	CDW14       uint32
	CDW15       uint32
	TimeoutMS   uint32
	Result      uint32
}

type discEntry struct {
	Trtype  uint8
	Adrfam  uint8
	Subtype uint8
	Treq    uint8
	PortID  uint16
	Cntlid  uint16
	Asqsz   uint16
	Trsvcid string
	Subnqn  string
	Traddr  string
	SecType uint8
}

type discLog struct {
	GenCtr  uint64
	NumRec  uint64
	RecFmt  uint16
	Entries []discEntry
}

func cString(b []byte) string {
	n := bytes.IndexByte(b, 0)
	if n < 0 {
		n = len(b)
	}
	return strings.TrimSpace(string(b[:n]))
}

func trtypeStr(v uint8) string {
	switch v {
	case 0:
		return "pcie"
	case 1:
		return "rdma"
	case 2:
		return "fc"
	case 3:
		return "tcp"
	case 254:
		return "loop"
	default:
		return fmt.Sprintf("%#x", v)
	}
}

func adrfamStr(v uint8) string {
	switch v {
	case 0:
		return "pci"
	case 1:
		return "ipv4"
	case 2:
		return "ipv6"
	case 3:
		return "ib"
	case 4:
		return "fc"
	default:
		return fmt.Sprintf("%#x", v)
	}
}

func subtypeStr(v uint8) string {
	switch v {
	case 1:
		return "discovery subsystem"
	case 2:
		return "nvme subsystem"
	case 3:
		return "current discovery subsystem"
	default:
		return fmt.Sprintf("%#x", v)
	}
}

func treqStr(v uint8) string {
	switch v & 0x3 {
	case 0:
		return "not specified"
	case 1:
		return "required"
	case 2:
		return "not required"
	default:
		return fmt.Sprintf("%#x", v)
	}
}

func sectypeStr(v uint8) string {
	switch v {
	case 0:
		return "none"
	case 1:
		return "tls"
	default:
		return fmt.Sprintf("%#x", v)
	}
}

func parseDiscoveryHeader(buf []byte) (genctr, numrec uint64, recfmt uint16, err error) {
	if len(buf) < 18 {
		return 0, 0, 0, fmt.Errorf("discovery log header too short: %d bytes", len(buf))
	}
	genctr = binary.LittleEndian.Uint64(buf[0:8])
	numrec = binary.LittleEndian.Uint64(buf[8:16])
	recfmt = binary.LittleEndian.Uint16(buf[16:18])
	if numrec > maxDiscEntries {
		return 0, 0, 0, fmt.Errorf("discovery log numrec %d out of range", numrec)
	}
	return genctr, numrec, recfmt, nil
}

func parseDiscoveryLog(buf []byte) (*discLog, error) {
	genctr, numrec, recfmt, err := parseDiscoveryHeader(buf)
	if err != nil {
		return nil, err
	}
	log := &discLog{GenCtr: genctr, NumRec: numrec, RecFmt: recfmt}
	n := int(numrec)
	if n == 0 {
		return log, nil
	}
	need := discHeaderSize + n*discEntrySize
	if len(buf) < need {
		return nil, fmt.Errorf("discovery log truncated: have %d want %d", len(buf), need)
	}
	for i := 0; i < n; i++ {
		off := discHeaderSize + i*discEntrySize
		e := buf[off : off+discEntrySize]
		log.Entries = append(log.Entries, discEntry{
			Trtype:  e[0],
			Adrfam:  e[1],
			Subtype: e[2],
			Treq:    e[3],
			PortID:  binary.LittleEndian.Uint16(e[4:6]),
			Cntlid:  binary.LittleEndian.Uint16(e[6:8]),
			Asqsz:   binary.LittleEndian.Uint16(e[8:10]),
			Trsvcid: cString(e[32:64]),
			Subnqn:  cString(e[256:512]),
			Traddr:  cString(e[512:768]),
			SecType: e[768],
		})
	}
	return log, nil
}

func (l *discLog) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Discovery Log Number of Records %d, Generation counter %d\n", l.NumRec, l.GenCtr)
	for i, e := range l.Entries {
		fmt.Fprintf(&b, "=====Discovery Log Entry %d======\n", i)
		fmt.Fprintf(&b, "trtype:  %s\n", trtypeStr(e.Trtype))
		fmt.Fprintf(&b, "adrfam:  %s\n", adrfamStr(e.Adrfam))
		fmt.Fprintf(&b, "subtype: %s\n", subtypeStr(e.Subtype))
		fmt.Fprintf(&b, "treq:    %s\n", treqStr(e.Treq))
		fmt.Fprintf(&b, "portid:  %d\n", e.PortID)
		fmt.Fprintf(&b, "cntlid:  %d\n", e.Cntlid)
		fmt.Fprintf(&b, "asqsz:   %d\n", e.Asqsz)
		fmt.Fprintf(&b, "trsvcid: %s\n", e.Trsvcid)
		fmt.Fprintf(&b, "subnqn:  %s\n", e.Subnqn)
		fmt.Fprintf(&b, "traddr:  %s\n", e.Traddr)
		if e.Trtype == 3 {
			fmt.Fprintf(&b, "sectype: %s\n", sectypeStr(e.SecType))
		}
	}
	return b.String()
}

func adminGetLog(fd int, lid uint8, offset uint64, data []byte) error {
	if len(data) == 0 || len(data)%4 != 0 {
		return fmt.Errorf("log transfer length %d is not a positive multiple of 4", len(data))
	}
	numd := uint32(len(data)/4 - 1)
	cmd := nvmePassthruCmd{
		Opcode:  nvmeAdminGetLogPage,
		Addr:    uint64(uintptr(unsafe.Pointer(&data[0]))),
		DataLen: uint32(len(data)),
		CDW10:   uint32(lid) | (numd << 16),
		CDW11:   numd >> 16,
		CDW12:   uint32(offset),
		CDW13:   uint32(offset >> 32),
	}
	r1, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(&cmd)
	if errno != 0 {
		return fmt.Errorf("NVME_IOCTL_ADMIN_CMD: %w", errno)
	}
	if r1 != 0 {
		return fmt.Errorf("get log page status: 0x%x", r1)
	}
	return nil
}

func getLogPage(fd int, lid uint8, buf []byte) error {
	off := 0
	for off < len(buf) {
		n := min(maxLogXfer, len(buf)-off)
		n -= n % 4
		if n == 0 {
			return fmt.Errorf("log transfer stuck at offset %d", off)
		}
		if err := adminGetLog(fd, lid, uint64(off), buf[off:off+n]); err != nil {
			return err
		}
		off += n
	}
	return nil
}

func getDiscoveryLog(instance int) (*discLog, error) {
	path := fmt.Sprintf("/dev/nvme%d", instance)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	fd := int(f.Fd())

	hdr := make([]byte, discHeaderSize)
	if err := getLogPage(fd, nvmeDiscoverLogID, hdr); err != nil {
		return nil, fmt.Errorf("discovery log header: %w", err)
	}
	genctr, numrec, recfmt, err := parseDiscoveryHeader(hdr)
	if err != nil {
		return nil, err
	}
	if numrec == 0 {
		return &discLog{GenCtr: genctr, NumRec: 0, RecFmt: recfmt}, nil
	}

	for range maxDiscRetries {
		size := discHeaderSize + int(numrec)*discEntrySize
		buf := make([]byte, size)
		if err := getLogPage(fd, nvmeDiscoverLogID, buf); err != nil {
			return nil, fmt.Errorf("discovery log: %w", err)
		}
		full, err := parseDiscoveryLog(buf)
		if err != nil {
			return nil, err
		}
		hdr2 := make([]byte, discHeaderSize)
		if err := getLogPage(fd, nvmeDiscoverLogID, hdr2); err != nil {
			return nil, fmt.Errorf("discovery log genctr: %w", err)
		}
		gen2, num2, _, err := parseDiscoveryHeader(hdr2)
		if err != nil {
			return nil, err
		}
		if gen2 == full.GenCtr && num2 == full.NumRec {
			return full, nil
		}
		genctr, numrec = gen2, num2
	}
	return nil, fmt.Errorf("discovery log changed during read")
}

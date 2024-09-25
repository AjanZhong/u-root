// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package universalpayload

import "encoding/binary"

func getPhysicalAddressSizes() (uint8, error) {
	// Return hardcode for arm64
	// Please update to actual physical address size
	return 44, nil
}

// Constrcut trampoline code before jump to entry point of FIT image.
// Due to lack of support to set value of register x0 in kexec,
// bootloader parameter needs to be prepared in trampoline code.
// Also stack is prepared in trampoline code snippet to ensure no data leak,
// and to ensure 64-bit entry code can use stack directly.
//
// Trampoline code snippet is prepared as following:
//
//	buf[0 - 3]   : 0x580000c4 - ldr x4, #0x18 (PC relative: buf[24 - 31])
//	buf[4 - 7]   : 0x580000e0 - ldr x0, #0x1c (PC relative: buf[32 - 39])
//	buf[8 - 11]  : 0xaa1f03e1 - mov x1, xzr
//	buf[12 - 15] : 0x580000e2 - ldr x2, #0x1c (PC relative: buf[40 - 47])
//	buf[16 - 19] : 0x9100005f - mov sp, x2
//	buf[20 - 23] : 0xd61f0080 - br  x4
//	buf[24 - 27] : uint32(uint64(entry)&0xffffffff))
//	buf[28 - 31] : uint32(uint64(entry)>>32))
//	buf[32 - 35] : uint32(uint64(hobAddr)&0xffffffff))
//	buf[36 - 39] : uint32(uint64(hobAddr)>>32))
//	buf[40 - 43] : uint32(uint64(stackTop)&0xffffffff))
//	buf[44 - 47] : uint32(uint64(stackTop)>>32))
func constructTrampoline(buf []uint8, hobAddr uint64, entry uint64) []uint8 {
	appendUint32 := func(slice []uint8, value uint32) []uint8 {
		tmpBytes := make([]uint8, 4)
		binary.LittleEndian.PutUint32(tmpBytes, value)
		return append(slice, tmpBytes...)
	}

	stackTop := hobAddr + tmpStackTop
	buf = appendUint32(buf, 0x580000c4)
	buf = appendUint32(buf, 0x580000e0)
	buf = appendUint32(buf, 0xaa1f03e1)
	buf = appendUint32(buf, 0x580000e2)
	buf = appendUint32(buf, 0x9100005f)
	buf = appendUint32(buf, 0xd61f0080)
	buf = appendUint32(buf, uint32(uint64(entry)&0xffffffff))
	buf = appendUint32(buf, uint32(uint64(entry)>>32))
	buf = appendUint32(buf, uint32(uint64(hobAddr)&0xffffffff))
	buf = appendUint32(buf, uint32(uint64(hobAddr)>>32))
	buf = appendUint32(buf, uint32(uint64(stackTop)&0xffffffff))
	buf = appendUint32(buf, uint32(uint64(stackTop)>>32))

	return buf
}

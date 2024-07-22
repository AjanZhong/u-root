// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !race
// +build !race

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hugelgupf/vmtest/qemu"
	"github.com/hugelgupf/vmtest/scriptvm"
	"github.com/hugelgupf/vmtest/testtmp"
	"github.com/u-root/mkuimage/uimage"
)

func TestKexecUpl(t *testing.T) {
	qemu.SkipIfNotArch(t, qemu.ArchAMD64, qemu.ArchArm64)

	initrd := filepath.Join(testtmp.TempDir(t), "initramfs.cpio")
	vm := scriptvm.Start(t, "vm", "kexec /upl",
		scriptvm.WithUimage(
			// Build kexec as a binary command to get accurate GOCOVERDIR
			// integration coverage data (busybox rewrites command code).
			uimage.WithCoveredCommands("github.com/u-root/u-root/cmds/core/kexec"),
			uimage.WithFiles(fmt.Sprintf("%s:upl", os.Getenv("VMTEST_UPL"))),
			uimage.WithCPIOOutput(initrd),
		),
		scriptvm.WithQEMUFn(
			qemu.WithVMTimeout(time.Minute),
			// Specify the machine type to report correct ACPI RSDP table
			qemu.ArbitraryArgs("-M", "q35"),
			qemu.ArbitraryArgs("-m", "8192"),
			// EDK2 requires processor to support TSC / Core Crystal ratio feature
			// Specify the processor type to meet abot EDK2 requirment.
			qemu.ArbitraryArgs("-cpu", "Skylake-Client"),
			qemu.WithInitramfs(initrd),
			// Initramfs available at /mount/9p/initramfs/initramfs.cpio.
			qemu.P9Directory(filepath.Dir(initrd), "initramfs"),
		),
	)

	// UEFI internal interactive shell prompts string ""Shell>".
	if _, err := vm.Console.ExpectString("Shell>"); err != nil {
		t.Fatal(err)
	}
	if err := vm.Kill(); err != nil {
		t.Errorf("Kill: %v", err)
	}
	_ = vm.Wait()
}

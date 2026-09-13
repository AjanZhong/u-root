// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && (!tinygo || tinygo.enable)

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/u-root/u-root/pkg/cmdline"
	"github.com/u-root/u-root/pkg/kmodule"
	"github.com/u-root/u-root/pkg/pty"
	"github.com/u-root/u-root/pkg/termios"
	"golang.org/x/sys/unix"
)

// USB HID keyboards inject keystrokes into the virtual console (tty0), not
// into the serial device. Keep these files alive so the kernel does not close
// the multiplexed session.
var consoleHold []*os.File

var usbInputModules = []string{
	"usbcore",
	"xhci_hcd",
	"xhci_pci",
	"ehci_hcd",
	"ehci_pci",
	"uhci_hcd",
	"ohci_hcd",
	"hid",
	"usbhid",
	"hid_generic",
}

func mergeConsoleNames(cmdlineConsoles []string) []string {
	seen := make(map[string]bool, len(cmdlineConsoles)+1)
	out := make([]string, 0, len(cmdlineConsoles)+2)
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range cmdlineConsoles {
		add(name)
	}
	// VGA/framebuffer and USB HID keyboards land on the current VT.
	add("tty0")
	if len(cmdlineConsoles) == 0 {
		add("ttyS0")
	}
	return out
}

func loadInputModules() {
	for _, name := range usbInputModules {
		if err := kmodule.Probe(name, ""); err != nil {
			log.Printf("module %s: %v", name, err)
			continue
		}
		log.Printf("loaded module %s", name)
	}
	// Give USB devices a moment to enumerate after the host controller loads.
	time.Sleep(500 * time.Millisecond)
}

func makeConsoleRaw(f *os.File) error {
	if err := termios.MakeRawFile(f); err != nil {
		return err
	}
	// MakeRawFile sets O_NONBLOCK; blocking reads keep keystrokes from being dropped.
	return unix.SetNonblock(int(f.Fd()), false)
}

func attachConsoles() error {
	names := mergeConsoleNames(cmdline.Consoles())
	var ttys []*os.File
	var opened []string
	for _, name := range names {
		path := filepath.Join("/dev", name)
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			log.Printf("open %s: %v", path, err)
			continue
		}
		if err := makeConsoleRaw(f); err != nil {
			log.Printf("raw %s: %v", path, err)
		}
		ttys = append(ttys, f)
		opened = append(opened, path)
	}
	if len(ttys) == 0 {
		return nil
	}

	ptmx, pts, err := pty.NewPTMS()
	if err != nil {
		for _, t := range ttys {
			t.Close()
		}
		return err
	}
	if err := unix.SetNonblock(int(pts.Fd()), false); err != nil {
		log.Printf("pty slave blocking: %v", err)
	}

	writers := make([]io.Writer, len(ttys))
	for i, t := range ttys {
		writers[i] = termios.NewRawWriter(t)
	}
	multi := io.MultiWriter(writers...)
	go io.Copy(multi, ptmx)
	for _, t := range ttys {
		go io.Copy(ptmx, t)
	}

	fd := int(pts.Fd())
	if err := unix.Dup2(fd, unix.Stdin); err != nil {
		return err
	}
	if err := unix.Dup2(fd, unix.Stdout); err != nil {
		return err
	}
	if err := unix.Dup2(fd, unix.Stderr); err != nil {
		return err
	}
	for _, stdfd := range []int{unix.Stdin, unix.Stdout, unix.Stderr} {
		if err := unix.SetNonblock(stdfd, false); err != nil {
			log.Printf("stdio blocking fd %d: %v", stdfd, err)
		}
	}

	os.Stdin = os.NewFile(uintptr(unix.Stdin), "/dev/stdin")
	os.Stdout = os.NewFile(uintptr(unix.Stdout), "/dev/stdout")
	os.Stderr = os.NewFile(uintptr(unix.Stderr), "/dev/stderr")
	log.SetOutput(os.Stderr)

	if err := unix.IoctlSetInt(fd, unix.TIOCSCTTY, 0); err != nil {
		log.Printf("TIOCSCTTY: %v", err)
	}

	consoleHold = append(consoleHold, ttys...)
	consoleHold = append(consoleHold, ptmx, pts)
	log.Printf("attached consoles: %v", opened)
	return nil
}

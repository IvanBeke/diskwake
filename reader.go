package main

import (
    "fmt"
    "unsafe"

    "golang.org/x/sys/unix"
)

// sectorSize is used both as the read size and the required alignment for
// O_DIRECT reads. 4096 works for essentially all modern drives (whether
// their physical sector size is 512e or 4Kn); it's a safe, conservative
// choice that satisfies alignment requirements in both cases.
const sectorSize = 4096

// keepAwake performs a single O_DIRECT read of the first sector of the
// given raw block device. Opening with O_DIRECT and reading straight from
// the device node (not a mounted filesystem path) guarantees the read
// bypasses the page cache and actually reaches the physical drive, which is
// what's needed to reset the drive/enclosure's own idle timer.
func keepAwake(device string) error {
    fd, err := unix.Open(device, unix.O_RDONLY|unix.O_DIRECT, 0)
    if err != nil {
        return fmt.Errorf("open %s: %w", device, err)
    }
    defer unix.Close(fd)

    buf := alignedBuffer(sectorSize, sectorSize)

    n, err := unix.Pread(fd, buf, 0)
    if err != nil {
        return fmt.Errorf("read %s: %w", device, err)
    }
    if n == 0 {
        return fmt.Errorf("read %s: zero bytes returned", device)
    }

    return nil
}

// alignedBuffer returns a []byte of the given size whose starting address
// is aligned to align bytes, as required by O_DIRECT. Go's garbage collector
// can relocate slices, but since this buffer is used synchronously within a
// single Pread call and not retained afterward, that's not a concern here.
func alignedBuffer(size, align int) []byte {
    buf := make([]byte, size+align)
    addr := uintptr(unsafe.Pointer(&buf[0]))
    offset := 0
    if rem := int(addr % uintptr(align)); rem != 0 {
        offset = align - rem
    }
    return buf[offset : offset+size]
}

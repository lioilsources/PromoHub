package media

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
)

// VideoInfo is what the hub checks before a video goes to a platform.
type VideoInfo struct {
	DurationMs int
	Width      int
	Height     int
}

// ProbeMP4 reads duration (mvhd) and the first video track's size (tkhd) from an MP4/MOV
// file without decoding it. The hub does no transcoding; this is only a pre-flight check.
func ProbeMP4(path string) (VideoInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return VideoInfo{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return VideoInfo{}, err
	}
	var vi VideoInfo
	found := false
	err = walk(f, 0, st.Size(), func(typ string, off, size int64) (bool, error) {
		switch typ {
		case "moov", "trak":
			return true, nil // descend
		case "mvhd":
			b := make([]byte, min(size, 32))
			if _, err := f.ReadAt(b, off); err != nil {
				return false, err
			}
			var scale, dur uint64
			if b[0] == 1 && len(b) >= 32 { // version 1: 64-bit times
				scale, dur = uint64(binary.BigEndian.Uint32(b[20:24])), binary.BigEndian.Uint64(b[24:32])
			} else if len(b) >= 20 {
				scale, dur = uint64(binary.BigEndian.Uint32(b[12:16])), uint64(binary.BigEndian.Uint32(b[16:20]))
			}
			if scale > 0 {
				vi.DurationMs = int(dur * 1000 / scale)
				found = true
			}
		case "tkhd":
			b := make([]byte, size)
			if _, err := f.ReadAt(b, off); err != nil {
				return false, err
			}
			if len(b) >= 8 {
				// width and height are the last 8 bytes, 16.16 fixed point; audio tracks have 0.
				w, h := int(binary.BigEndian.Uint32(b[len(b)-8:])>>16), int(binary.BigEndian.Uint32(b[len(b)-4:])>>16)
				if w > 0 && h > 0 && vi.Width == 0 {
					vi.Width, vi.Height = w, h
				}
			}
		}
		return false, nil
	})
	if err != nil {
		return vi, err
	}
	if !found {
		return vi, errors.New("not an MP4 (no moov/mvhd box)")
	}
	return vi, nil
}

// walk visits boxes in [start, end); visit returns true to descend into a container box.
func walk(r io.ReaderAt, start, end int64, visit func(typ string, payload, size int64) (bool, error)) error {
	hdr := make([]byte, 16)
	for off := start; off+8 <= end; {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return err
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		head := int64(8)
		switch size {
		case 1:
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return err
			}
			size, head = int64(binary.BigEndian.Uint64(hdr[8:16])), 16
		case 0:
			size = end - off
		}
		if size < head || off+size > end {
			return errors.New("corrupt MP4 box")
		}
		down, err := visit(typ, off+head, size-head)
		if err != nil {
			return err
		}
		if down {
			if err := walk(r, off+head, off+size, visit); err != nil {
				return err
			}
		}
		off += size
	}
	return nil
}

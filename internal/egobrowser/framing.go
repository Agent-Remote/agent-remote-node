package egobrowser

import (
	"encoding/binary"
	"errors"
	"io"
)

const maxIPCFrameBytes = 16 * 1024 * 1024

var errIPCFrameTooLarge = errors.New("ego-browser broker frame exceeds limit")

func readFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > maxIPCFrameBytes {
		return nil, errIPCFrameTooLarge
	}
	frame := make([]byte, int(length))
	if _, err := io.ReadFull(reader, frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func writeFrame(writer io.Writer, frame []byte) error {
	if len(frame) > maxIPCFrameBytes || uint64(len(frame)) > uint64(^uint32(0)) {
		return errIPCFrameTooLarge
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(frame)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, frame)
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

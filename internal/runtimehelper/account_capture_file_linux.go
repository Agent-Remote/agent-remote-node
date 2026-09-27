package runtimehelper

import (
	"context"
	"errors"
	"net"
	"os"

	"github.com/Agent-Remote/agent-remote-node/internal/skillmanager"
	"golang.org/x/sys/unix"
)

func readCaptureMessage(connection *net.UnixConn, data, oob []byte) (int, int, int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, 0, 0, err
	}
	var n, ancillary, flags int
	var receiveErr error
	err = raw.Read(func(fd uintptr) bool {
		for {
			n, ancillary, flags, _, receiveErr = unix.Recvmsg(int(fd), data, oob, unix.MSG_CMSG_CLOEXEC)
			if receiveErr != unix.EINTR {
				break
			}
		}
		return receiveErr != unix.EAGAIN && receiveErr != unix.EWOULDBLOCK
	})
	if err != nil {
		return 0, 0, 0, err
	}
	return n, ancillary, flags, receiveErr
}

func (e Engine) openAccountCaptureFile(ctx context.Context, request Request) (*os.File, accountCaptureFileResponse, error) {
	payload, err := validateCaptureFileRequest(ctx, request, e.config.NodeID)
	if err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	store, err := e.openExistingSkillStateRoot()
	if err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	defer store.Close()
	if payload.Kind == "manifest" {
		file, capture, err := skillmanager.OpenAccountCaptureManifest(store, payload.Binding)
		if err != nil {
			return nil, accountCaptureFileResponse{}, err
		}
		metadata, err := captureFileMetadata(file, capture, payload.Kind, nil)
		if err != nil {
			_ = file.Close()
			return nil, metadata, err
		}
		return file, metadata, nil
	}
	bundle, capture, _, err := skillmanager.OpenAccountCapture(store, payload.Binding)
	if err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	_ = bundle.Close()
	if capture.HelperReceiptID != payload.HelperReceiptID || capture.TreeDigest != payload.TreeDigest {
		return nil, accountCaptureFileResponse{}, errors.New("capture object identity changed")
	}
	file, entry, err := skillmanager.OpenAccountCaptureObject(store, payload.Binding, payload.Digest)
	if err != nil {
		return nil, accountCaptureFileResponse{}, err
	}
	metadata, err := captureFileMetadata(file, capture, payload.Kind, &entry)
	if err != nil {
		_ = file.Close()
		return nil, metadata, err
	}
	return file, metadata, nil
}

func validateCaptureFileDescriptor(file *os.File, size int64) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return err
	}
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	if flags&unix.O_ACCMODE != unix.O_RDONLY || flags&unix.O_PATH != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o600 || stat.Uid != 0 || stat.Nlink != 1 || stat.Size != size {
		return errors.New("unsafe retained capture descriptor")
	}
	_, err = file.Seek(0, 0)
	return err
}

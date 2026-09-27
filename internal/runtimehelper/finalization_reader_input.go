package runtimehelper

import (
	"bufio"
	"context"
	"io"
	"net"
)

func withFinalizationReaderInput(cancel context.CancelFunc, connection net.Conn, input io.Reader, run func(*bufio.Reader)) {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := io.Copy(writer, input)
		_ = writer.CloseWithError(err)
		cancel()
	}()
	defer func() {
		_ = reader.Close()
		_ = connection.Close()
		<-done
	}()
	run(bufio.NewReader(reader))
}

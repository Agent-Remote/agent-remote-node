package runtimehelper

import "errors"

var errAccountCopyPending = errors.New("STATE_COPY_PENDING: retained account backup requires recovery")
var errAccountCopyFailed = errors.New("STATE_COPY_FAILED: account backup failed; retained source and partial backup require inspection")

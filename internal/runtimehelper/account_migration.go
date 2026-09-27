package runtimehelper

import "errors"

var errMigrationWritersUnknown = errors.New("STATE_MIGRATION_PENDING: migration writer termination or retained outcome requires recovery")
var errMigrationFailed = errors.New("STATE_MIGRATION_FAILED: retained backend migration failed")

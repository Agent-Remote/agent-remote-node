package skillmanager

import (
	"errors"
	"os"
	"strconv"
)

func requireCopyWriterOutcome(root *os.Root, copy AccountCopyReceipt, outcome string) error {
	if copy.WriterVersion == 0 {
		return nil
	}
	saved, err := readMigrationWriter(root, migrationWriterName(copy.TaskID, "copy"))
	copy.State = "started"
	if err != nil || saved.Identity.Copy != copy || saved.State != outcome {
		return errors.New("copy writer completion is unavailable")
	}
	return nil
}

func requireMigrationWriterOutcomes(root *os.Root, migration AccountMigrationReceipt, outcome string) error {
	if migration.Copy.WriterVersion == 0 {
		return nil
	}
	if err := requireMigrationOwnershipOutcomes(root, migration, outcome); err != nil {
		return err
	}
	if err := requireCopyWriterOutcome(root, migration.Copy, "succeeded"); err != nil {
		return err
	}
	for _, phase := range []string{"target", "rollback"} {
		required := outcome == "succeeded" && phase == "target" || outcome == "failed" && phase == "rollback"
		for index := 0; index < 3; index++ {
			writer, err := readMigrationWriter(root, migrationWriterName(migration.Copy.TaskID, phase+"-"+strconv.Itoa(index)))
			if errors.Is(err, os.ErrNotExist) && !required {
				continue
			}
			if err == nil && outcome == "succeeded" && phase == "rollback" {
				return errors.New("successful migration contains rollback writers")
			}
			if err != nil || writer.Identity.Copy != migration.Copy || (writer.State != "succeeded" && writer.State != "failed") || required && writer.State != "succeeded" {
				return errors.New("migration phase writer evidence is incomplete")
			}
		}
	}
	return nil
}

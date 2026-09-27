package runtimehelper

import "regexp"

var skillUUIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func validSkillUUID(value string) bool {
	return skillUUIDPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

package handlers

import (
	"capstan/internal/database"
	"capstan/internal/services"
)

type outcome int

// lifecycleStatus is a seeded result.
func lifecycleStatus(o outcome) string {
	if o == 0 {
		return string(services.StackStatusRunning)
	}
	return "partial" // want `bare "partial" is returned as a services.StackStatus carrier`
}

// A seeded parameter of another package's method.
func persist(db *database.DB, id string) {
	_ = db.UpdateStackStatus(id, "stopped") // want `bare "stopped" is passed as a services.StackStatus carrier \(status\)`
	_ = db.UpdateStackStatus(id, lifecycleStatus(1))
}

// A local that flows into a seeded parameter is compared with a literal.
func deletePolicy(db *database.DB, targetType, id string) {
	if targetType != "container" && targetType != string(services.JobTargetTypeStack) { // want `bare "container" is compared with a services.JobTargetType carrier \(targetType\)`
		return
	}
	_ = db.DeleteAutoUpdatePolicy(targetType, id)
}

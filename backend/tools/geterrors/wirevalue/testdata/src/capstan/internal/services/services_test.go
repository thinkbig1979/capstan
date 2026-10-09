package services

import "capstan/internal/models"

// _test.go files are skipped: fixtures spell wire values on purpose.
func fixture(stack *models.Stack) { stack.Status = "running" }

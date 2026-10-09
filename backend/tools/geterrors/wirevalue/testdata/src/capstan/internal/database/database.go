package database

type DB struct{}

func (d *DB) UpdateStackStatus(id, status string) error { return nil }

func (d *DB) DeleteAutoUpdatePolicy(targetType, targetID string) error { return nil }

func (d *DB) GetAutoUpdatePolicy(targetType, targetID string) error { return nil }

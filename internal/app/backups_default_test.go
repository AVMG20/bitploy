package app

import (
	"context"
	"testing"
	"time"
)

// Automatic backups are on by default for an app with something to back up,
// and never turned on again once a user turned them off.
func TestDefaultBackups(t *testing.T) {
	e := newBkEnv(t)
	ctx := context.Background()
	get := func(slug string) BackupSettings {
		t.Helper()
		l, err := e.s.ListBackups(ctx, e.user, slug)
		if err != nil {
			t.Fatal(err)
		}
		return l.Settings
	}

	// Made with /data: daily, kept 14 days, first backup at its own time.
	notes := e.mustApp(t, CreateAppInput{Slug: "notes", Volume: true, VolumeMB: ptr(int64(100))})
	st := get("notes")
	if st.Schedule != "daily" || st.KeepMode != "days" || st.Keep != 14 || st.NextAt == nil {
		t.Fatalf("new app with /data: %+v", st)
	}
	if want := latestSlot("daily", backupMinute(notes.ID), e.clock).AddDate(0, 0, 1); !st.NextAt.Equal(want) {
		t.Errorf("next = %v, want %v (the first backup time after it was turned on)", st.NextAt, want)
	}
	e.exec(t, `UPDATE apps SET current_deployment_id = 'dep_seed' WHERE id = ?`, notes.ID)
	e.runDue(ctx)
	if n := e.countScheduled(t, notes.ID); n != 0 {
		t.Fatalf("a default schedule made %d backups at once", n)
	}
	e.clock = e.clock.Add(24 * time.Hour)
	e.runDue(ctx)
	if n := e.countScheduled(t, notes.ID); n != 1 {
		t.Fatalf("after its backup time: %d automatic backups", n)
	}
	// Changing only the retention keeps the schedule.
	if st, err := e.s.UpdateBackupSettings(ctx, e.user, "notes", BackupSettingsInput{Keep: ptr(int64(30))}); err != nil || st.Schedule != "daily" || st.Keep != 30 {
		t.Fatalf("keep 30: %+v, %v", st, err)
	}

	// Without /data: nothing until /data is turned on.
	if e.mustApp(t, CreateAppInput{Slug: "site"}); get("site").Schedule != "off" {
		t.Errorf("app without /data: %+v", get("site"))
	}
	if _, err := e.s.UpdateApp(ctx, e.user, "site", UpdateAppInput{Volume: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if st := get("site"); st.Schedule != "daily" || st.Keep != 14 {
		t.Errorf("after /data was turned on: %+v", st)
	}

	// Turned off by the user: stays off when /data goes off and on again,
	// and the backup loop leaves it alone.
	if _, err := e.s.UpdateBackupSettings(ctx, e.user, "site", BackupSettingsInput{Schedule: ptr("off")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, "site", UpdateAppInput{Volume: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateApp(ctx, e.user, "site", UpdateAppInput{Volume: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	e.runDue(ctx)
	if st := get("site"); st.Schedule != "off" {
		t.Errorf("turned off by the user, then: %+v", st)
	}

	// Services: a database gets them, a cache does not.
	p, err := e.s.CreateProject(ctx, e.user, CreateProjectInput{Name: "Shop"})
	if err != nil {
		t.Fatal(err)
	}
	pg, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if st := get(pg.Slug); st.Schedule != "daily" || st.Keep != 14 {
		t.Errorf("postgres service: %+v", st)
	}
	vk, err := e.s.AddService(ctx, e.user, p.ID, AddServiceInput{Template: "valkey"})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Q.GetApp(ctx, vk.ID)
	if row.BackupSchedule != "off" || row.BackupScheduleSet != 0 {
		t.Errorf("valkey service: %s set=%d", row.BackupSchedule, row.BackupScheduleSet)
	}
	e.runDue(ctx)
	if row, _ := e.s.Q.GetApp(ctx, vk.ID); row.BackupSchedule != "off" {
		t.Errorf("the backup loop turned on backups of valkey")
	}

	// An app the loop finds without a schedule (a compose deploy gave it a
	// volume) gets the default there.
	e.exec(t, `UPDATE apps SET backup_schedule = 'off', backup_keep_mode = 'last', backup_keep = 7, backup_schedule_set = 0 WHERE slug = 'notes'`)
	e.runDue(ctx)
	if st := get("notes"); st.Schedule != "daily" || st.Keep != 14 {
		t.Errorf("after the loop: %+v", st)
	}
}

func (e *bkEnv) countScheduled(t *testing.T, appID string) int {
	t.Helper()
	var n int
	if err := e.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM app_backups WHERE app_id = ? AND kind = 'scheduled'`, appID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

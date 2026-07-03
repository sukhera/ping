ALTER TABLE alerts DROP COLUMN IF EXISTS is_reminder;
ALTER TABLE monitors DROP COLUMN IF EXISTS reminder_every_s;

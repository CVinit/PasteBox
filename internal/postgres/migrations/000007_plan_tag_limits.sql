ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS tags_per_paste_limit integer NOT NULL DEFAULT 0;

UPDATE plans SET tags_per_paste_limit = 5 WHERE id = 'plus';
UPDATE plans SET tags_per_paste_limit = 20 WHERE id = 'pro';


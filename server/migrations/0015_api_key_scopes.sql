-- Empty array = unrestricted (all endpoints the role allows), which keeps existing keys working.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scopes TEXT[] NOT NULL DEFAULT '{}';

-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema


-- One row per iOS device that receives the chat pushes of its user over
-- APNs in the fork-apns-push experiment. The token is the key, so a phone
-- that signs in as another user moves to that user. The flags are the
-- device's choice of which chat events to push. The token is stored in lower
-- case hex only, so one device never has two rows.
CREATE TABLE chat_apns_devices (
    device_token text PRIMARY KEY CHECK (device_token ~ '^[0-9a-f]+$'),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    environment text NOT NULL CHECK (environment IN ('sandbox', 'production')),
    finished_turns boolean NOT NULL,
    questions boolean NOT NULL,
    agent_errors boolean NOT NULL,
    sound boolean NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE INDEX chat_apns_devices_user_id_idx ON chat_apns_devices (user_id);

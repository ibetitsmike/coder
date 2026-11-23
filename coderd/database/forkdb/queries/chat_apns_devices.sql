-- Registers an APNs device for a user, or updates it. A token that belonged
-- to another user moves to this one.
-- name: UpsertChatAPNsDevice :exec
INSERT INTO chat_apns_devices (
    device_token,
    user_id,
    environment,
    finished_turns,
    questions,
    agent_errors,
    sound,
    created_at,
    updated_at
) VALUES (
    @device_token,
    @user_id,
    @environment,
    @finished_turns,
    @questions,
    @agent_errors,
    @sound,
    @now,
    @now
)
ON CONFLICT (device_token) DO UPDATE SET
    user_id = EXCLUDED.user_id,
    environment = EXCLUDED.environment,
    finished_turns = EXCLUDED.finished_turns,
    questions = EXCLUDED.questions,
    agent_errors = EXCLUDED.agent_errors,
    sound = EXCLUDED.sound,
    updated_at = EXCLUDED.updated_at;

-- Gets one device of a user.
-- name: GetChatAPNsDevice :one
SELECT *
FROM chat_apns_devices
WHERE user_id = @user_id
    AND device_token = @device_token;

-- Lists the devices of a user, for a push.
-- name: GetChatAPNsDevicesByUserID :many
SELECT *
FROM chat_apns_devices
WHERE user_id = @user_id
ORDER BY created_at, device_token;

-- Unregisters a device of a user. Deleting a token that is not registered,
-- or that belongs to another user, deletes nothing.
-- name: DeleteChatAPNsDevice :exec
DELETE FROM chat_apns_devices
WHERE user_id = @user_id
    AND device_token = @device_token;

-- Deletes a token that APNs reported as no longer valid, unless the device
-- registered again after not_after, so a newer registration survives a
-- rejection of an older push.
-- name: DeleteChatAPNsDeviceByToken :exec
DELETE FROM chat_apns_devices
WHERE device_token = @device_token
    AND updated_at <= @not_after;

-- Records the environment that APNs accepted a token in, unless the device
-- registered again after not_after.
-- name: UpdateChatAPNsDeviceEnvironment :exec
UPDATE chat_apns_devices
SET environment = @environment,
    updated_at = @updated_at
WHERE device_token = @device_token
    AND updated_at <= @not_after;

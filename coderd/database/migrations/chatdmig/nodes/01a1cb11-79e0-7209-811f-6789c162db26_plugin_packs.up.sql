-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

CREATE TABLE plugin_packs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    source_url text NOT NULL,
    source_path text NOT NULL DEFAULT '',
    ref text NOT NULL,
    commit_sha text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

COMMENT ON TABLE plugin_packs IS 'Plugin packs installed from a Git repository and pinned to one commit.';
COMMENT ON COLUMN plugin_packs.source_path IS 'Directory of the pack in the repository. Empty for the repository root.';
COMMENT ON COLUMN plugin_packs.ref IS 'Tag, branch or commit that the install or the last update resolved.';
COMMENT ON COLUMN plugin_packs.commit_sha IS 'Commit that the stored resources come from.';

CREATE TABLE plugin_pack_resources (
    pack_id uuid NOT NULL REFERENCES plugin_packs(id) ON DELETE CASCADE,
    path text NOT NULL,
    content bytea NOT NULL,
    content_hash text NOT NULL,
    PRIMARY KEY (pack_id, path)
);

COMMENT ON COLUMN plugin_pack_resources.path IS 'Slash-separated path relative to the pack directory.';
COMMENT ON COLUMN plugin_pack_resources.content_hash IS 'Hex SHA-256 of content.';

-- requires: 01a1cb11-79e0-7209-811f-6789c162db26_plugin_packs

CREATE TABLE plugin_pack_sources (
    organization_id uuid PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    hosts text[] NOT NULL DEFAULT '{}',
    updated_at timestamp with time zone NOT NULL DEFAULT now()
);

COMMENT ON TABLE plugin_pack_sources IS 'Git hosts that organization admins allow plugin packs to be installed from. An organization without a row allows none.';

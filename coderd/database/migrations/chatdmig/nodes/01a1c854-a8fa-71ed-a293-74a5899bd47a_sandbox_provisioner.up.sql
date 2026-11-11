-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema

-- The core enum stays in public, and forkdb.ProvisionerTypeSandbox holds the
-- Go side, like forkdb.APIKeyScopeValues does for api_key_scope.
ALTER TYPE public.provisioner_type ADD VALUE IF NOT EXISTS 'sandbox';

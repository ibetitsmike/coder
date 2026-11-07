-- requires: upstream:mcp_server_oauth2_client_registration
-- search_path: public, chatd
--
-- Moves the Coder Agents objects that upstream created in public into the
-- chatd schema. SET SCHEMA changes the catalog only: no row is copied, and
-- indexes, constraints, triggers and owned sequences move with each table.
-- Functions keep unqualified names in their bodies, so every connection must
-- have chatd on its search path (migrations.WithChatdSearchPath).
DO $adopt$
DECLARE
	r record;
BEGIN
	FOR r IN
		SELECT c.relname, c.relkind
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()
			AND c.relkind IN ('r', 'p', 'v', 'm')
			AND c.relname ~ '^(chats|chats_.+|chat_.+|skills|mcp_server_(configs|user_tokens)|organization_member_(disabled_skills|skill_settings)|user_ai_provider_keys)$'
	LOOP
		EXECUTE format(
			CASE r.relkind
				WHEN 'v' THEN 'ALTER VIEW %I SET SCHEMA chatd'
				WHEN 'm' THEN 'ALTER MATERIALIZED VIEW %I SET SCHEMA chatd'
				ELSE 'ALTER TABLE %I SET SCHEMA chatd'
			END, r.relname);
	END LOOP;

	-- No table owns some sequences, for example
	-- chat_queued_messages_position_seq, so they do not move with a table.
	FOR r IN
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind = 'S' AND c.relname ~ '^chat_'
	LOOP
		EXECUTE format('ALTER SEQUENCE %I SET SCHEMA chatd', r.relname);
	END LOOP;

	FOR r IN
		SELECT t.typname
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = current_schema() AND t.typtype = 'e' AND t.typname ~ '^chat_'
	LOOP
		EXECUTE format('ALTER TYPE %I SET SCHEMA chatd', r.typname);
	END LOOP;

	FOR r IN
		SELECT p.oid::regprocedure AS sig
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = current_schema() AND p.proname ~ '(chat|skill|mcp_server)'
	LOOP
		EXECUTE format('ALTER FUNCTION %s SET SCHEMA chatd', r.sig);
	END LOOP;
END
$adopt$;

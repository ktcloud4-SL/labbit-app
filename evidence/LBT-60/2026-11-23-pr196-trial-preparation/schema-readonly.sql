-- Preparation query ONLY; not executed on shared RDS in this packet.
-- Run through reviewed exact-host/TLS/read-only credentials; never put DSN in argv/logs.
BEGIN READ ONLY;
SET LOCAL statement_timeout = '2s';
SET LOCAL lock_timeout = '1s';
SELECT clock_timestamp() AS observed_at_utc, current_database(), session_user, pg_is_in_recovery();
SELECT version, name, checksum, applied_at FROM public.labbit_schema_migrations ORDER BY version;
SELECT to_regclass('public.worker_control_states') AS states,
       to_regclass('public.worker_control_changes') AS changes,
       to_regclass('public.worker_control_boundaries') AS boundaries,
       to_regclass('public.worker_control_resumes') AS resumes,
       to_regclass('public.worker_control_restrictions') AS restrictions;
ROLLBACK;

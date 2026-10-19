BEGIN;

-- Durable restoration after a rejected RESET or a guaranteed unsent dispatch.
ALTER TABLE operation_items ADD COLUMN previous_instance_status text NULL;

COMMIT;

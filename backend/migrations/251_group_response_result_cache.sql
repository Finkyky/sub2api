ALTER TABLE groups ADD COLUMN IF NOT EXISTS response_result_cache_config JSONB NOT NULL
 DEFAULT '{"enabled":false,"ttl_seconds":30,"max_response_bytes":2097152,"allow_group_sharing":false,"revision":1}'::jsonb;
DO $result_cache_constraint$
BEGIN
 IF NOT EXISTS (
  SELECT 1 FROM pg_constraint
  WHERE conname = 'groups_result_cache_config_object'
    AND conrelid = 'groups'::regclass
 ) THEN
  ALTER TABLE groups ADD CONSTRAINT groups_result_cache_config_object
   CHECK (jsonb_typeof(response_result_cache_config) = 'object');
 END IF;
END;
$result_cache_constraint$;

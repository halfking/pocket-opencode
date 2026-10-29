-- Additive, opt-in provenance. Never backfill portal_managed on existing rows.
ALTER TABLE rss_sources ADD COLUMN IF NOT EXISTS portal_managed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE rss_sources ADD COLUMN IF NOT EXISTS portal_epoch BIGINT CHECK(portal_epoch BETWEEN 0 AND 9007199254740991);
CREATE TABLE IF NOT EXISTS rss_portable_changes (
 sequence BIGSERIAL PRIMARY KEY CHECK(sequence BETWEEN 1 AND 9007199254740991),user_id TEXT NOT NULL,workspace_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('source','item')),ext_id TEXT NOT NULL,title TEXT NOT NULL,
 payload JSONB NOT NULL,client_updated_at TIMESTAMPTZ,op_id TEXT NOT NULL UNIQUE,
 epoch BIGINT CHECK(epoch BETWEEN 0 AND 9007199254740991),deleted BOOLEAN NOT NULL,
 intent TEXT NOT NULL,changed_fields JSONB NOT NULL,sync_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_rss_portable_scope_cursor ON rss_portable_changes(user_id,workspace_id,sequence);
CREATE OR REPLACE FUNCTION rss_portable_source_change() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE src rss_sources%ROWTYPE; changed JSONB; at TIMESTAMPTZ;
BEGIN
 IF TG_OP='DELETE' THEN src:=OLD; ELSE src:=NEW; END IF;
 IF NOT src.portal_managed THEN RETURN COALESCE(NEW,OLD); END IF;
 IF TG_OP='UPDATE' THEN
  changed:='[]'::jsonb;
  IF NEW.title IS DISTINCT FROM OLD.title THEN changed:=changed||'"title"'::jsonb; END IF;
  IF NEW.url IS DISTINCT FROM OLD.url THEN changed:=changed||'"url"'::jsonb; END IF;
  IF NEW.category IS DISTINCT FROM OLD.category THEN changed:=changed||'"category"'::jsonb; END IF;
  IF changed='[]'::jsonb THEN RETURN NEW; END IF;
 ELSE changed:='["title","url","category"]'::jsonb; END IF;
 -- Scheduler bookkeeping already owns the source row. It must return before
 -- taking the scope lock, which portable writers acquire before row locks.
 PERFORM pg_advisory_xact_lock(hashtextextended(json_build_array('rss-portable',src.user_id,src.workspace_id)::text,0));
 at:=clock_timestamp();
 INSERT INTO rss_portable_changes(user_id,workspace_id,kind,ext_id,title,payload,client_updated_at,op_id,epoch,deleted,intent,changed_fields)
 VALUES(src.user_id,src.workspace_id,'source','pocketd-source:'||src.id,src.title,
  CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE jsonb_build_object('url',src.url,'category',src.category) END,
  at,'pocketd-rss:'||gen_random_uuid()::text,src.portal_epoch,TG_OP='DELETE',lower(TG_OP),changed);
 IF TG_OP='DELETE' THEN
  INSERT INTO rss_portable_changes(user_id,workspace_id,kind,ext_id,title,payload,client_updated_at,op_id,epoch,deleted,intent,changed_fields)
  SELECT src.user_id,src.workspace_id,'item','pocketd-item:'||id,title,'{}'::jsonb,at,'pocketd-rss:'||gen_random_uuid()::text,src.portal_epoch,TRUE,'delete','[]'::jsonb
  FROM rss_items WHERE source_id=src.id AND user_id=src.user_id AND workspace_id=src.workspace_id;
  RETURN OLD;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS rss_portable_source_write ON rss_sources;
CREATE TRIGGER rss_portable_source_write AFTER INSERT OR UPDATE ON rss_sources FOR EACH ROW EXECUTE FUNCTION rss_portable_source_change();
DROP TRIGGER IF EXISTS rss_portable_source_delete ON rss_sources;
CREATE TRIGGER rss_portable_source_delete BEFORE DELETE ON rss_sources FOR EACH ROW EXECUTE FUNCTION rss_portable_source_change();
CREATE OR REPLACE FUNCTION rss_portable_item_change() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
DECLARE src rss_sources%ROWTYPE;
BEGIN
 SELECT * INTO src FROM rss_sources WHERE id=NEW.source_id AND user_id=NEW.user_id AND workspace_id=NEW.workspace_id;
 IF NOT FOUND OR NOT src.portal_managed THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND NEW.title IS NOT DISTINCT FROM OLD.title AND NEW.url IS NOT DISTINCT FROM OLD.url AND NEW.summary IS NOT DISTINCT FROM OLD.summary AND NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(json_build_array('rss-portable',src.user_id,src.workspace_id)::text,0));
 INSERT INTO rss_portable_changes(user_id,workspace_id,kind,ext_id,title,payload,client_updated_at,op_id,epoch,deleted,intent,changed_fields,sync_error)
 VALUES(src.user_id,src.workspace_id,'item','pocketd-item:'||NEW.id,NEW.title,jsonb_build_object('source',src.id,'url',NEW.url,'summary',NEW.summary),NEW.updated_at,
  'pocketd-rss:'||gen_random_uuid()::text,src.portal_epoch,FALSE,'feed','["title","source","url","summary"]'::jsonb,
  CASE WHEN NEW.updated_at IS NULL THEN 'source_time_unknown' ELSE '' END);
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS rss_portable_item_write ON rss_items;
CREATE TRIGGER rss_portable_item_write AFTER INSERT OR UPDATE ON rss_items FOR EACH ROW EXECUTE FUNCTION rss_portable_item_change();

CREATE TABLE telegram_integration (
 id integer PRIMARY KEY CHECK(id=1),
 bot_id bigint NOT NULL,
 username text NOT NULL,
 token_nonce bytea NOT NULL,
 token_ciphertext bytea NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 update_offset bigint NOT NULL DEFAULT 0,
 lease_owner text NOT NULL DEFAULT '',
 lease_until timestamptz,
 last_error text NOT NULL DEFAULT '',
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE telegram_preferences (
 user_id text PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 chat_id bigint UNIQUE,
 telegram_name text NOT NULL DEFAULT '',
 enabled boolean NOT NULL DEFAULT true,
 completed boolean NOT NULL DEFAULT true,
 failed boolean NOT NULL DEFAULT true,
 pairing_hash text UNIQUE,
 pairing_expires_at timestamptz,
 last_test_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE telegram_outbox (
 id text PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 experiment_id text REFERENCES experiments(id) ON DELETE CASCADE,
 event text NOT NULL CHECK(event IN ('completed','failed','test')),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','sending','sent','failed')),
 attempts integer NOT NULL DEFAULT 0,
 available_at timestamptz NOT NULL DEFAULT now(),
 lease_id text NOT NULL DEFAULT '',
 lease_until timestamptz,
 last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(experiment_id,event)
);
CREATE INDEX telegram_outbox_pending ON telegram_outbox(available_at) WHERE status IN ('pending','sending');
CREATE FUNCTION notify_experiment_telegram() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status IN ('succeeded','failed') AND NEW.kind<>'comparison' THEN
  INSERT INTO telegram_outbox(id,user_id,experiment_id,event)
  SELECT 'tg_'||NEW.id||'_'||NEW.status,NEW.owner_user_id,NEW.id,
   CASE WHEN NEW.status='succeeded' THEN 'completed' ELSE 'failed' END
  FROM telegram_preferences p,telegram_integration b,users u
  WHERE u.id=p.user_id AND u.enabled AND p.user_id=NEW.owner_user_id AND p.chat_id IS NOT NULL AND p.enabled AND b.enabled
   AND ((NEW.status='succeeded' AND p.completed) OR (NEW.status='failed' AND p.failed))
  ON CONFLICT(experiment_id,event) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER experiment_telegram_terminal AFTER UPDATE OF status ON experiments
 FOR EACH ROW EXECUTE FUNCTION notify_experiment_telegram();

-- Legacy event identities cannot be reconstructed safely from current state.
-- Leave their hashes null: replay requires reviewed source provenance.
ALTER TABLE consumed_events ADD COLUMN payload_hash text
 CHECK (payload_hash IS NULL OR payload_hash ~ '^[0-9a-f]{64}$');

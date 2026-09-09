ALTER TABLE fulfillment_evidence ADD COLUMN sha256 text;
ALTER TABLE fulfillment_evidence ADD COLUMN verified_at timestamptz;
ALTER TABLE fulfillment_evidence ADD CONSTRAINT evidence_verification_pair CHECK (
 (sha256 IS NULL AND verified_at IS NULL) OR
 (sha256 IS NOT NULL AND sha256 ~ '^[0-9a-f]{64}$' AND verified_at IS NOT NULL)
);

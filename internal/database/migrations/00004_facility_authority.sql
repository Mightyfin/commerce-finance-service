-- Unknown legacy provenance must not grant authority to bind a delivery.
ALTER TABLE eligible_facilities ADD COLUMN caller_application_id text NOT NULL DEFAULT '';
ALTER TABLE eligible_facilities ADD COLUMN recipient_relationship_id text NOT NULL DEFAULT '';
ALTER TABLE eligible_facilities ADD COLUMN party_id text NOT NULL DEFAULT '';

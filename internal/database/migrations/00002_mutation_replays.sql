CREATE TABLE commerce_replays (
 action_id text PRIMARY KEY REFERENCES commerce_actions(id),
 response jsonb NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now()
);
-- Fulfillment details are immutable; only status changes. Reconstruct historical
-- responses using the recorded action's status, never today's disputed status.
INSERT INTO commerce_replays(action_id,response)
SELECT a.id,jsonb_build_object(
 'id',f.id,'facility_id',f.facility_id,'recipient_participant_id',f.recipient_participant_id,
 'status',CASE a.action WHEN 'confirmed' THEN 'confirmed' ELSE 'disputed' END,
 'currency',f.currency,'total_value',to_char(f.total_value_minor::numeric/100,'FM9999999999999999990.00'),
 'delivery_confirmed_at',f.delivery_confirmed_at,'created_at',f.created_at,
 'items',COALESCE((SELECT jsonb_agg(jsonb_build_object('description',i.description,'value',to_char(i.value_minor::numeric/100,'FM9999999999999999990.00')) ORDER BY i.id) FROM fulfillment_items i WHERE i.fulfillment_id=f.id),'[]'::jsonb),
 'evidence',COALESCE((SELECT jsonb_agg(jsonb_build_object('document_id',e.document_id,'type',e.evidence_type) ORDER BY e.id) FROM fulfillment_evidence e WHERE e.fulfillment_id=f.id),'[]'::jsonb)
)
FROM commerce_actions a JOIN fulfillments f ON f.id=a.fulfillment_id;
CREATE TRIGGER commerce_replays_immutable BEFORE UPDATE OR DELETE ON commerce_replays
FOR EACH ROW EXECUTE FUNCTION prevent_commerce_action_mutation();

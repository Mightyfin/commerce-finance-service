# Commerce UAT release gates

Local changes are not a deployed certification. The GitHub destination is still
unconfigured; preserve this service in its own repository.

## Migrations and broker cutover

Apply migrations 00002 (immutable mutation replay snapshots), 00003 (facility
source fingerprints), 00004 (facility authority) and 00005 (verified evidence)
after a database backup. Validate the upgrade against a
populated copy before sandbox deployment. Old consumed events retain null hashes:
their original payload cannot be recovered from the current projection. Replaying
one requires reviewed source provenance, not a guessed fingerprint or clearing
deduplication records.

Deploy the Commerce producer before the eFaaS Commerce webhook consumer. New facts
carry authenticated tenant/application identity; the publisher supplies deployment
environment and rejects conflicts. Historical callerless facts are quarantined by
the bridge. Do not replay the entire historical stream without a cutover plan.

## Semantics tenants must understand

Confirmation records fulfillment, not merchant settlement, credit utilization,
document-content certification or an automatic authorization to transfer funds.
A dispute opens a record; it does not reverse a financial posting or issue a refund.
Webhooks expose only fulfillment ID and status. Read details through authorized APIs.

## Implemented locally; release verification required

- Confirm the facility source carries its original application, participant
  relationship and party. Unknown historical authority fails closed. Current
  confirmations require this same participant; delegated recipient models are
  not implicitly allowed.
- Configure COMMERCE_FINANCE_DOCUMENT_SERVICE_BASE_URL for the API. Confirm the
  caller has commerce:write, documents.evidence.verify and the Document Service
  token audience. Do not grant scopes automatically.
- Verify clean document versions using delegated tenant/environment credentials
  and the Document Service evidence-verification endpoint. New records store
  the digest and verification time. Original retry responses remain unchanged.
- Keep fulfillment separate from any future reserved capacity, approved payee,
  disbursement or settlement orchestration; those need authoritative owning services.
- Test permission failures, missing/dirty/deleted documents, foreign parties,
  cross-application access, duplicates, partial-service failures and recovery.

Disposable database and HTTP tests cover authority conflicts, unavailable and
mismatched documents, delegated credentials, forbidden redirects and retry
preservation. Public cross-service UAT is still required. Old records without
verification metadata remain unverified; do not label them verified retrospectively.

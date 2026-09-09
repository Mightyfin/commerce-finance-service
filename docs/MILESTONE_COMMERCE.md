# Commerce Finance Capability Milestone

Scope: fulfilment registration with verified clean document versions, originating-application and facility-participant checks, tenant-safe list/get, and idempotent dispute opening. These checks are implemented locally; coordinated deployment and public UAT remain release gates. Historical unverified records are not upgraded by inference. See UAT-RELEASE-GATES.md.

Controls:

- only facility lifecycle events populate the eligibility projection;
- at least one document reference and one positive-value item are mandatory;
- tenant and calling application scope every public read;
- create and dispute actions are append-only audit evidence;
- idempotency hashes reject key reuse with different requests;
- domain events use an outbox and JetStream message IDs;
- no endpoint or worker can move money or change a credit facility;
- production remains unavailable.

Delivered release evidence:

- sandbox composition for API, PostgreSQL, migration, facility consumer and outbox publisher;
- database-backed tenant/application isolation and changed-payload idempotency tests;
- released OpenAPI and facility/commerce event contracts;
- `commerce:read` and `commerce:write` identity scopes and EFaaS gateway routes;
- runtime create, list, replay, currency guard, dispute and event-publication checks.

Production remains unavailable. Production dispute resolution, remediation and any financial movement require a separate controlled milestone.

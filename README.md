# Commerce Finance Service

Owns tenant-scoped fulfilment evidence and dispute state for facilities used in embedded-commerce workflows.

It consumes authoritative facility lifecycle events and only accepts fulfilments linked to an authorised or disbursed facility. A fulfilment must contain item values, delivery time, recipient participant, and document evidence. It emits commerce events through an outbox.

This service has no Wallet, Payment Rails, pricing, decision, or ledger client. Confirming or disputing fulfilment therefore cannot create, authorize, settle, reverse, or recalculate a financial exposure.

Current milestone status: sandbox deployed behind the EFaaS gateway with released contracts, OAuth scopes, facility-event projection, runtime replay tests and transactional outbox publishing. Production remains unavailable.

-- The verification standard (internal/domain/intel/verification.go).
--
-- Until now an endpoint became 'verified' on its own, the first time a paid
-- call delivered to its pinned payee. That proves a transaction worked, not
-- who the seller is: anyone can stand up an endpoint, charge a tenth of a
-- cent and answer. From here on a delivery makes an endpoint "payment tested"
-- (tested_at), and 'verified' means a person reviewed evidence of who runs it
-- after every automatic check passed.
--
-- So every endpoint that verified itself goes back to unverified, with the
-- reason on its history. Its tested_at is kept: that part was true. Endpoints
-- a person decided on (trust_reason 'MANUAL') are left as they are.

INSERT INTO provider_changes (id, endpoint_id, provider_id, source, kind, suspicious, before, after, actor, created_at)
SELECT 'pchg_' || gen_random_uuid(), id, provider_id, source, 'trust_changed', false,
       jsonb_build_object('trust', 'verified', 'reason', trust_reason),
       jsonb_build_object('trust', 'unverified', 'reason', 'STANDARD_CHANGED',
                          'note', 'A paid delivery no longer verifies an endpoint. It stays payment tested; verification needs a review of who runs it.'),
       'migration:0024', now()
FROM provider_endpoints
WHERE trust = 'verified' AND trust_reason = 'PAID_DELIVERY_MATCHED';

UPDATE provider_endpoints
SET trust = 'unverified', trust_reason = '', verified_at = NULL, updated_at = now()
WHERE trust = 'verified' AND trust_reason = 'PAID_DELIVERY_MATCHED';

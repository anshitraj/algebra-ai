-- Purchase memory (internal/app/purchase_memory.go): before paying for work,
-- Algebra looks for a delivered purchase of the same work with the same input
-- by the same person, recent enough to hand back again. This index answers
-- that, and the repeat-purchase count, without scanning a person's intents.
CREATE INDEX IF NOT EXISTS idx_economic_intents_memory
    ON economic_intents(principal_id, capability, input_hash)
    WHERE state = 'COMMITTED' AND fulfillment = 'FULFILLED';

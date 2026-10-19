-- Immutable prompt contracts and lineage bindings cannot be discarded by downgrade.
SELECT RAISE(ABORT,'prompt contracts migration cannot be downgraded');

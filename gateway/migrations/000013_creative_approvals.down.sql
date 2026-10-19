-- Human audit and immutable artifact history cannot be discarded by downgrade.
SELECT RAISE(ABORT,'creative approvals migration cannot be downgraded');

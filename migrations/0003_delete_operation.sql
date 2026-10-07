-- P3.8: deleting an app is an operation. The worker removes the app's routes,
-- containers, network, and images first, then the app row, whose cascade
-- takes the rest, this operation included. The audit event stays.
ALTER TABLE operations DROP CONSTRAINT operations_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_kind_check
	CHECK (kind IN ('deploy', 'rollback', 'delete'));

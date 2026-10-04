-- P5.4 (ADR-0012): rotating a KEK re-wraps each value's data key under the
-- new KEK. A secret value stays immutable in everything that defines it
-- (its ID, app, key, ciphertext, and creation time); only the pair
-- (wrapped_dek, kek_id) may change, both at once: a new KEK and a new
-- wrapping. Environment revisions keep pointing at the same rows, so
-- nothing they mean changes.
CREATE FUNCTION shipyard_secret_rewrap_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
	IF (NEW.id, NEW.app_id, NEW.key, NEW.ciphertext, NEW.created_at)
	       IS DISTINCT FROM (OLD.id, OLD.app_id, OLD.key, OLD.ciphertext, OLD.created_at)
	   OR NEW.kek_id = OLD.kek_id OR NEW.wrapped_dek = OLD.wrapped_dek THEN
		RAISE EXCEPTION 'UPDATE on secret_values may only re-wrap the data key under another KEK'
			USING ERRCODE = 'SY001';
	END IF;
	RETURN NEW;
END
$$;

DROP TRIGGER secret_values_immutable ON secret_values;
CREATE TRIGGER secret_values_rewrap_only BEFORE UPDATE ON secret_values
	FOR EACH ROW EXECUTE FUNCTION shipyard_secret_rewrap_only();

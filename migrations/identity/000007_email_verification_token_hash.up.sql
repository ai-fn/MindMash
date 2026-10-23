-- Raw tokens cannot be hashed in place; outstanding links simply expire.
TRUNCATE email_verifications;
ALTER TABLE email_verifications RENAME COLUMN token TO token_hash;
ALTER TABLE email_verifications ADD CONSTRAINT email_verifications_token_hash_len CHECK (length(token_hash) = 64);

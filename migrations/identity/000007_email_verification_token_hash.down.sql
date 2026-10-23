TRUNCATE email_verifications;
ALTER TABLE email_verifications DROP CONSTRAINT email_verifications_token_hash_len;
ALTER TABLE email_verifications RENAME COLUMN token_hash TO token;

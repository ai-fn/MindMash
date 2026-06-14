package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// dbtx is satisfied by both *pgxpool.Pool and pgx.Tx, allowing the same query
// methods to work inside or outside a transaction without duplication.
type dbtx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PostgresRepository implements Repository over a pgxpool.Pool.
type PostgresRepository struct {
	pool *pgxpool.Pool
	db   dbtx
}

// NewPostgresRepository returns a repository that uses the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, db: pool}
}

// withDB returns a shallow copy of the repository using the given dbtx.
// Used internally so a tx-scoped repo shares all method logic.
func (r *PostgresRepository) withDB(db dbtx) *PostgresRepository {
	return &PostgresRepository{pool: r.pool, db: db}
}

// ─── Transaction support ─────────────────────────────────────────────────────

// WithTx runs fn inside a database transaction.  The Repository passed to fn
// uses the transaction connection; the outer repository is unchanged.
func (r *PostgresRepository) WithTx(ctx context.Context, fn func(Repository) error) error {
	return platformdb.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		return fn(r.withDB(tx))
	})
}

// ─── Error helpers ───────────────────────────────────────────────────────────

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// mapNoRows maps pgx.ErrNoRows to a platform NotFound error; other errors pass through.
func mapNoRows(err error, msg string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platformerrors.NotFound(msg)
	}
	return err
}

// ─── User ────────────────────────────────────────────────────────────────────

func (r *PostgresRepository) CreateUser(ctx context.Context, u *User) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO users (id, email, email_verified, password_hash, locale, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID, u.Email, u.EmailVerified, u.PasswordHash, u.Locale, u.Status, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("user with this email already exists")
		}
		return fmt.Errorf("identity: create user: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetUserByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, email_verified, password_hash, locale, status, created_at, updated_at
		FROM users WHERE id = $1`, id).
		Scan(&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.Locale, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get user by id: %w", mapNoRows(err, "user not found"))
	}
	return u, nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, email_verified, password_hash, locale, status, created_at, updated_at
		FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.Locale, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get user by email: %w", mapNoRows(err, "user not found"))
	}
	return u, nil
}

func (r *PostgresRepository) SetEmailVerified(ctx context.Context, userID string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET email_verified = true, updated_at = now() WHERE id = $1`, userID)
	if err != nil {
		return fmt.Errorf("identity: set email verified: %w", err)
	}
	return nil
}

func (r *PostgresRepository) UpdatePassword(ctx context.Context, userID, hash string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("identity: update password: %w", err)
	}
	return nil
}

// ─── OAuth ───────────────────────────────────────────────────────────────────

func (r *PostgresRepository) CreateOAuthIdentity(ctx context.Context, o *OAuthIdentity) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO oauth_identities (id, user_id, provider, provider_uid, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		o.ID, o.UserID, o.Provider, o.ProviderUID, o.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return platformerrors.AlreadyExists("oauth identity already exists")
		}
		return fmt.Errorf("identity: create oauth identity: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetOAuthIdentity(ctx context.Context, provider, uid string) (*OAuthIdentity, error) {
	o := &OAuthIdentity{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, provider, provider_uid, created_at
		FROM oauth_identities WHERE provider = $1 AND provider_uid = $2`, provider, uid).
		Scan(&o.ID, &o.UserID, &o.Provider, &o.ProviderUID, &o.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get oauth identity: %w", mapNoRows(err, "oauth identity not found"))
	}
	return o, nil
}

// ─── Profile ─────────────────────────────────────────────────────────────────

func (r *PostgresRepository) CreateProfile(ctx context.Context, p *Profile) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO profiles (id, user_id, name, is_default, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		p.ID, p.UserID, p.Name, p.IsDefault, p.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("identity: create profile: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ListProfiles(ctx context.Context, userID string) ([]Profile, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, name, is_default, created_at
		FROM profiles WHERE user_id = $1 ORDER BY created_at ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("identity: list profiles: %w", err)
	}
	defer rows.Close()

	var profiles []Profile
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.IsDefault, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("identity: list profiles scan: %w", err)
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: list profiles rows: %w", err)
	}
	return profiles, nil
}

func (r *PostgresRepository) GetProfile(ctx context.Context, id string) (*Profile, error) {
	p := &Profile{}
	err := r.db.QueryRow(ctx, `
		SELECT id, user_id, name, is_default, created_at
		FROM profiles WHERE id = $1`, id).
		Scan(&p.ID, &p.UserID, &p.Name, &p.IsDefault, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get profile: %w", mapNoRows(err, "profile not found"))
	}
	return p, nil
}

// ─── RefreshToken ────────────────────────────────────────────────────────────

func (r *PostgresRepository) CreateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO refresh_tokens (jti, user_id, profile_id, family_id, expires_at, revoked, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		rt.JTI, rt.UserID, rt.ProfileID, rt.FamilyID, rt.ExpiresAt, rt.Revoked, rt.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("identity: create refresh token: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetRefreshTokenForUpdate(ctx context.Context, jti string) (*RefreshToken, error) {
	rt := &RefreshToken{}
	err := r.db.QueryRow(ctx, `
		SELECT jti, user_id, profile_id, family_id, expires_at, revoked, created_at
		FROM refresh_tokens WHERE jti = $1 FOR UPDATE`, jti).
		Scan(&rt.JTI, &rt.UserID, &rt.ProfileID, &rt.FamilyID, &rt.ExpiresAt, &rt.Revoked, &rt.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get refresh token for update: %w", mapNoRows(err, "refresh token not found"))
	}
	return rt, nil
}

func (r *PostgresRepository) RevokeRefreshToken(ctx context.Context, jti string) error {
	_, err := r.db.Exec(ctx, `UPDATE refresh_tokens SET revoked = true WHERE jti = $1`, jti)
	if err != nil {
		return fmt.Errorf("identity: revoke refresh token: %w", err)
	}
	return nil
}

func (r *PostgresRepository) RevokeFamily(ctx context.Context, familyID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		UPDATE refresh_tokens SET revoked = true
		WHERE family_id = $1 AND revoked = false
		RETURNING jti`, familyID)
	if err != nil {
		return nil, fmt.Errorf("identity: revoke family: %w", err)
	}
	defer rows.Close()

	var jtis []string
	for rows.Next() {
		var jti string
		if err := rows.Scan(&jti); err != nil {
			return nil, fmt.Errorf("identity: revoke family scan: %w", err)
		}
		jtis = append(jtis, jti)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: revoke family rows: %w", err)
	}
	return jtis, nil
}

// ─── EmailVerification ───────────────────────────────────────────────────────

func (r *PostgresRepository) CreateEmailVerification(ctx context.Context, ev *EmailVerification) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO email_verifications (token, user_id, purpose, expires_at, consumed_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		ev.Token, ev.UserID, ev.Purpose, ev.ExpiresAt, ev.ConsumedAt, ev.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("identity: create email verification: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetEmailVerification(ctx context.Context, token string) (*EmailVerification, error) {
	ev := &EmailVerification{}
	err := r.db.QueryRow(ctx, `
		SELECT token, user_id, purpose, expires_at, consumed_at, created_at
		FROM email_verifications WHERE token = $1`, token).
		Scan(&ev.Token, &ev.UserID, &ev.Purpose, &ev.ExpiresAt, &ev.ConsumedAt, &ev.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("identity: get email verification: %w", mapNoRows(err, "verification token not found"))
	}
	return ev, nil
}

func (r *PostgresRepository) ConsumeEmailVerification(ctx context.Context, token string) error {
	now := time.Now()
	_, err := r.db.Exec(ctx, `UPDATE email_verifications SET consumed_at = $1 WHERE token = $2`, now, token)
	if err != nil {
		return fmt.Errorf("identity: consume email verification: %w", err)
	}
	return nil
}

package identity

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	identityv1 "github.com/mindmesh/mindmesh/api/identity/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// Handler implements all three gRPC service interfaces by delegating to
// *Service.  A single struct is used to keep wiring simple; it embeds the
// three Unimplemented* types for forward-compatibility.
type Handler struct {
	identityv1.UnimplementedAuthServiceServer
	identityv1.UnimplementedProfileServiceServer
	identityv1.UnimplementedInternalAuthServiceServer

	svc *Service
}

// NewHandler constructs a Handler wrapping the given Service.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ─── AuthService ─────────────────────────────────────────────────────────────

func (h *Handler) Register(ctx context.Context, req *identityv1.RegisterRequest) (*identityv1.RegisterResponse, error) {
	userID, err := h.svc.Register(ctx, req.Email, req.Password, req.Locale)
	if err != nil {
		return nil, err
	}
	return &identityv1.RegisterResponse{
		UserId:  userID,
		Message: "registration successful; please verify your email",
	}, nil
}

func (h *Handler) Login(ctx context.Context, req *identityv1.LoginRequest) (*identityv1.LoginResponse, error) {
	access, refresh, expiresIn, userID, profileID, err := h.svc.Login(ctx, req.Email, req.Password)
	if err != nil {
		return nil, err
	}
	return &identityv1.LoginResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		UserId:       userID,
		ProfileId:    profileID,
		ExpiresIn:    expiresIn,
	}, nil
}

func (h *Handler) OAuthExchange(ctx context.Context, req *identityv1.OAuthExchangeRequest) (*identityv1.OAuthExchangeResponse, error) {
	access, refresh, expiresIn, userID, profileID, isNew, err := h.svc.OAuthExchange(ctx, req.Provider, req.Code, req.RedirectUri)
	if err != nil {
		return nil, err
	}
	return &identityv1.OAuthExchangeResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		UserId:       userID,
		ProfileId:    profileID,
		ExpiresIn:    expiresIn,
		IsNewUser:    isNew,
	}, nil
}

func (h *Handler) Refresh(ctx context.Context, req *identityv1.RefreshRequest) (*identityv1.RefreshResponse, error) {
	access, refresh, expiresIn, err := h.svc.Refresh(ctx, req.RefreshToken)
	if err != nil {
		return nil, err
	}
	return &identityv1.RefreshResponse{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
	}, nil
}

func (h *Handler) Logout(ctx context.Context, req *identityv1.LogoutRequest) (*identityv1.LogoutResponse, error) {
	h.svc.Logout(ctx, req.RefreshToken)
	return &identityv1.LogoutResponse{Message: "logged out"}, nil
}

func (h *Handler) GetMe(ctx context.Context, _ *identityv1.GetMeRequest) (*identityv1.GetMeResponse, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, platformerrors.Unauthenticated("missing identity context")
	}

	user, profiles, err := h.svc.GetMe(ctx, id.UserID)
	if err != nil {
		return nil, err
	}

	protoProfiles := make([]*identityv1.ProfileInfo, len(profiles))
	for i, p := range profiles {
		protoProfiles[i] = profileToProto(p)
	}

	return &identityv1.GetMeResponse{
		UserId:        user.ID,
		Email:         user.Email,
		EmailVerified: user.EmailVerified,
		Locale:        user.Locale,
		Profiles:      protoProfiles,
	}, nil
}

func (h *Handler) VerifyEmail(ctx context.Context, req *identityv1.VerifyEmailRequest) (*identityv1.VerifyEmailResponse, error) {
	if err := h.svc.VerifyEmail(ctx, req.Token); err != nil {
		return nil, err
	}
	return &identityv1.VerifyEmailResponse{Message: "email verified"}, nil
}

func (h *Handler) RequestPasswordReset(ctx context.Context, req *identityv1.RequestPasswordResetRequest) (*identityv1.RequestPasswordResetResponse, error) {
	if err := h.svc.RequestPasswordReset(ctx, req.Email); err != nil {
		return nil, err
	}
	return &identityv1.RequestPasswordResetResponse{Message: "if the account exists, a reset link was sent"}, nil
}

func (h *Handler) ResetPassword(ctx context.Context, req *identityv1.ResetPasswordRequest) (*identityv1.ResetPasswordResponse, error) {
	if err := h.svc.ResetPassword(ctx, req.Token, req.NewPassword); err != nil {
		return nil, err
	}
	return &identityv1.ResetPasswordResponse{Message: "password reset"}, nil
}

// ─── ProfileService ──────────────────────────────────────────────────────────

func (h *Handler) ListProfiles(ctx context.Context, _ *identityv1.ListProfilesRequest) (*identityv1.ListProfilesResponse, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, platformerrors.Unauthenticated("missing identity context")
	}

	profiles, err := h.svc.ListProfiles(ctx, id.UserID)
	if err != nil {
		return nil, err
	}

	protoProfiles := make([]*identityv1.ProfileInfo, len(profiles))
	for i, p := range profiles {
		protoProfiles[i] = profileToProto(p)
	}
	return &identityv1.ListProfilesResponse{Profiles: protoProfiles}, nil
}

func (h *Handler) CreateProfile(ctx context.Context, req *identityv1.CreateProfileRequest) (*identityv1.CreateProfileResponse, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, platformerrors.Unauthenticated("missing identity context")
	}

	profile, err := h.svc.CreateProfile(ctx, id.UserID, req.Name)
	if err != nil {
		return nil, err
	}
	return &identityv1.CreateProfileResponse{Profile: profileToProto(*profile)}, nil
}

func (h *Handler) ActivateProfile(ctx context.Context, req *identityv1.ActivateProfileRequest) (*identityv1.ActivateProfileResponse, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, platformerrors.Unauthenticated("missing identity context")
	}

	accessToken, expiresIn, err := h.svc.ActivateProfile(ctx, id.UserID, req.Id)
	if err != nil {
		return nil, err
	}
	return &identityv1.ActivateProfileResponse{
		AccessToken: accessToken,
		ProfileId:   req.Id,
		ExpiresIn:   expiresIn,
	}, nil
}

// ─── InternalAuthService ─────────────────────────────────────────────────────

func (h *Handler) ValidateToken(ctx context.Context, req *identityv1.ValidateTokenRequest) (*identityv1.ValidateTokenResponse, error) {
	userID, profileID, locale, roles, err := h.svc.ValidateToken(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	return &identityv1.ValidateTokenResponse{
		UserId:    userID,
		ProfileId: profileID,
		Locale:    locale,
		Roles:     roles,
	}, nil
}

func (h *Handler) GetProfile(ctx context.Context, req *identityv1.GetProfileRequest) (*identityv1.GetProfileResponse, error) {
	profile, err := h.svc.GetProfile(ctx, req.ProfileId)
	if err != nil {
		return nil, err
	}
	return &identityv1.GetProfileResponse{
		Id:        profile.ID,
		UserId:    profile.UserID,
		Name:      profile.Name,
		IsDefault: profile.IsDefault,
	}, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func profileToProto(p Profile) *identityv1.ProfileInfo {
	return &identityv1.ProfileInfo{
		Id:        p.ID,
		Name:      p.Name,
		IsDefault: p.IsDefault,
		CreatedAt: timestamppb.New(p.CreatedAt),
	}
}

// Package domain defines the core domain types for the identity service.
package domain

import (
	identityv1 "milton_prism/pkg/pb/gen/milton_prism/types/identity/v1"
	tokenv1 "milton_prism/pkg/pb/gen/milton_prism/types/token/v1"
)

// Aliases proto — single source of truth, no mapping layer.
type (
	User                = identityv1.User
	UsersFilter         = identityv1.UsersFilter
	UserState           = identityv1.UserState
	AuthorizationTokens = tokenv1.AuthorizationTokens
)

// Re-export commonly used enum values.
const (
	UserStateUnspecified = identityv1.UserState_USER_STATE_UNSPECIFIED
	UserStateActive      = identityv1.UserState_USER_STATE_ACTIVE
	UserStateSuspended   = identityv1.UserState_USER_STATE_SUSPENDED
	UserStateDeleted     = identityv1.UserState_USER_STATE_DELETED
)

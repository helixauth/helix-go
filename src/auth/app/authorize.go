package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/helixauth/core/src/auth/app/oauth"
	"github.com/helixauth/core/src/entity"
	"github.com/helixauth/core/src/lib/database"
	"github.com/helixauth/core/src/lib/mapper"
	"github.com/helixauth/core/src/lib/token"
	"github.com/helixauth/core/src/lib/utils"

	"github.com/dchest/uniuri"
	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type formInput struct {
	Email           string  `form:"email" binding:"required"`
	Password        *string `form:"password"`
	ConfirmPassword *string `form:"confirm_password"`
}

// Authorize is the handler for the /authorize endpoint
func (a *app) Authorize(c *gin.Context) {

	// Parse OAuth params
	params := oauth.Params{}
	if err := c.BindQuery(&params); err != nil {
		c.HTML(
			http.StatusBadRequest,
			"error.html",
			gin.H{"error": err.Error()},
		)
		return
	}

	// Validate OAuth params
	ctx := a.context(c)
	if err := a.validateOAuthParams(ctx, params); err != nil {
		c.HTML(
			http.StatusBadRequest,
			"error.html",
			gin.H{"error": err.Error()},
		)
		return
	}

	// Process request
	switch c.Request.Method {
	case http.MethodGet:
		render(c, params, nil, nil)

	case http.MethodPost:
		form := formInput{}
		if err := c.Bind(&form); err != nil {
			render(c, params, nil, err)
			return
		}
		dest, err := a.processForm(ctx, params, form)
		if err != nil {
			render(c, params, &form, err)
		} else {
			c.Redirect(http.StatusFound, dest)
		}

	default:
		render(c, params, nil, nil)
	}
}

func (a *app) processForm(ctx context.Context, params oauth.Params, form formInput) (string, error) {

	// Query for existing users with the email address in the form
	userNotFound := false
	user := &entity.User{}
	err := a.Database.QueryItem(ctx, user, "SELECT * FROM users WHERE email = $1", form.Email)
	if err == sql.ErrNoRows {
		userNotFound = true
	} else if err != nil {
		return "", err
	}

	txn, err := a.Database.BeginTxn(ctx)
	if err != nil {
		return "", err
	}

	// Register a new user or authenticate the existing user
	if userNotFound {
		if params.IsSignUp() {
			user, err = a.registerUser(ctx, params, form, txn)
		} else {
			return "", fmt.Errorf("Incorrect email or password")
		}
	} else {
		err = a.authenticateUser(user, form)
	}
	if err != nil {
		return "", err
	}

	err = txn.Commit()
	if err != nil {
		return "", err
	}

	// Start a new user session
	code, err := a.generateAuthorizationCode(ctx, params, user)
	if err != nil {
		return "", err
	}

	// Redirect to the provided redirect URI with session code and state
	dest := fmt.Sprintf("https://%v?code=%v", mapper.String(params.RedirectURI), code)
	if params.State != nil {
		dest = fmt.Sprintf("%v&state=%v", dest, *params.State)
	}
	return dest, nil
}

// registerUser creates a new user
func (a *app) registerUser(ctx context.Context, params oauth.Params, form formInput, txn database.Txn) (*entity.User, error) {
	passwordHash, err := utils.HashPassword(form.Password)
	if err != nil {
		return nil, err
	}
	user := &entity.User{
		ID:            uniuri.NewLen(40),
		TenantID:      a.TenantID,
		Email:         &form.Email,
		EmailVerified: mapper.BoolPtr(false),
		PasswordHash:  passwordHash,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	err = txn.Insert(ctx, user)
	return user, err
}

// authenticateUser validates the form input against an existing user's records
func (a *app) authenticateUser(user *entity.User, form formInput) error {
	if user.PasswordHash != nil {
		if form.Password != nil {
			if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(*form.Password)); err != nil {
				return fmt.Errorf("Incorrect email or password")
			}
		} else {
			return fmt.Errorf("Password required")
		}
	} else {
		// TODO send email verification
	}
	return nil
}

func (a *app) validateOAuthParams(ctx context.Context, params oauth.Params) error {
	client, err := a.getClient(ctx, params.ClientID)
	if err != nil {
		return fmt.Errorf("'client_id' is invalid")
	}

	if params.RedirectURI == nil {
		return fmt.Errorf("'redirect_uri' is invalid")
	}

	isRedirectURIAuthorized := false
	for _, uri := range client.AuthorizedDomains {
		if *params.RedirectURI == uri {
			isRedirectURIAuthorized = true
		}
	}

	if !isRedirectURIAuthorized {
		return fmt.Errorf("'redirect_uri' is invalid")
	}

	return nil
}

// getClient fetches a client for the client ID
func (a *app) getClient(ctx context.Context, clientID string) (*entity.Client, error) {
	client := &entity.Client{}
	err := a.Database.QueryItem(ctx, client, `SELECT * FROM clients WHERE id = $1`, clientID)
	return client, err
}

// render renders the authorization form on screen
func render(c *gin.Context, params oauth.Params, form *formInput, err error) {
	tmplParams := gin.H{
		"action":   fmt.Sprintf(`%v?%v`, c.Request.URL.RawPath, c.Request.URL.RawQuery),
		"email":    nil,
		"password": nil,
		"error":    nil,
	}
	if form != nil {
		tmplParams["email"] = form.Email
		tmplParams["password"] = form.Password
	}
	if err != nil {
		tmplParams["error"] = err.Error()
	}

	// Render the 'sign up' page
	if params.IsSignUp() {
		tmplParams["title"] = "Sign up"
		c.HTML(http.StatusOK, "signUp.html", tmplParams)
		return
	}

	// Render the 'sign in' page
	tmplParams["title"] = "Sign in"
	c.HTML(http.StatusOK, "signIn.html", tmplParams)
}

// generateAuthorizationCode generates an authorization code for the authorization_code grant flow
func (a *app) generateAuthorizationCode(ctx context.Context, params oauth.Params, user *entity.User) (string, error) {
	claims := map[string]interface{}{
		"jti":          uniuri.NewLen(uniuri.UUIDLen),
		"client_id":    params.ClientID,
		"redirect_uri": params.RedirectURI,
		"user_id":      user.ID,
	}
	exp := time.Now().UTC().Add(30 * time.Second)
	return token.JWT(ctx, claims, exp, jwt.SigningMethodHS256, a.Secrets)
}

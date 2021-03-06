package okta

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-errors/errors"
	"github.com/hashicorp/vault/helper/policyutil"
	"github.com/hashicorp/vault/logical"
	"github.com/hashicorp/vault/logical/framework"
)

func pathLogin(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: `login/(?P<username>.+)`,
		Fields: map[string]*framework.FieldSchema{
			"username": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: "Username to be used for login.",
			},

			"password": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: "Password for this user.",
			},
		},

		Callbacks: map[logical.Operation]framework.OperationFunc{
			logical.UpdateOperation:         b.pathLogin,
			logical.AliasLookaheadOperation: b.pathLoginAliasLookahead,
		},

		HelpSynopsis:    pathLoginSyn,
		HelpDescription: pathLoginDesc,
	}
}

func (b *backend) pathLoginAliasLookahead(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	username := d.Get("username").(string)
	if username == "" {
		return nil, fmt.Errorf("missing username")
	}

	return &logical.Response{
		Auth: &logical.Auth{
			Alias: &logical.Alias{
				Name: username,
			},
		},
	}, nil
}

func (b *backend) pathLogin(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	username := d.Get("username").(string)
	password := d.Get("password").(string)

	policies, resp, groupNames, err := b.Login(ctx, req, username, password)
	// Handle an internal error
	if err != nil {
		return nil, err
	}
	if resp != nil {
		// Handle a logical error
		if resp.IsError() {
			return resp, nil
		}
	} else {
		resp = &logical.Response{}
	}

	sort.Strings(policies)

	cfg, err := b.getConfig(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.Auth = &logical.Auth{
		Policies: policies,
		Metadata: map[string]string{
			"username": username,
			"policies": strings.Join(policies, ","),
		},
		InternalData: map[string]interface{}{
			"password": password,
		},
		DisplayName: username,
		LeaseOptions: logical.LeaseOptions{
			TTL:       cfg.TTL,
			Renewable: true,
		},
		Alias: &logical.Alias{
			Name: username,
		},
	}

	if resp.Auth.TTL == 0 {
		resp.Auth.TTL = b.System().DefaultLeaseTTL()
	}
	if cfg.MaxTTL > 0 {
		maxTTL := cfg.MaxTTL
		if maxTTL > b.System().MaxLeaseTTL() {
			maxTTL = b.System().MaxLeaseTTL()
		}

		if resp.Auth.TTL > maxTTL {
			resp.Auth.TTL = maxTTL
			resp.AddWarning(fmt.Sprintf("Effective TTL of '%s' exceeded the effective max_ttl of '%s'; TTL value is capped accordingly", resp.Auth.TTL, maxTTL))
		}
	}

	for _, groupName := range groupNames {
		if groupName == "" {
			continue
		}
		resp.Auth.GroupAliases = append(resp.Auth.GroupAliases, &logical.Alias{
			Name: groupName,
		})
	}

	return resp, nil
}

func (b *backend) pathLoginRenew(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	username := req.Auth.Metadata["username"]
	password := req.Auth.InternalData["password"].(string)

	loginPolicies, resp, groupNames, err := b.Login(ctx, req, username, password)
	if len(loginPolicies) == 0 {
		return resp, err
	}

	if !policyutil.EquivalentPolicies(loginPolicies, req.Auth.Policies) {
		return nil, fmt.Errorf("policies have changed, not renewing")
	}

	cfg, err := b.getConfig(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err = framework.LeaseExtend(cfg.TTL, cfg.MaxTTL, b.System())(ctx, req, d)
	if err != nil {
		return nil, err
	}

	// Remove old aliases
	resp.Auth.GroupAliases = nil

	for _, groupName := range groupNames {
		resp.Auth.GroupAliases = append(resp.Auth.GroupAliases, &logical.Alias{
			Name: groupName,
		})
	}

	return resp, nil

}

func (b *backend) getConfig(ctx context.Context, req *logical.Request) (*ConfigEntry, error) {

	cfg, err := b.Config(ctx, req.Storage)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("Okta backend not configured")
	}

	return cfg, nil
}

const pathLoginSyn = `
Log in with a username and password.
`

const pathLoginDesc = `
This endpoint authenticates using a username and password.
`

# SAML identity providers: use an OIDC bridge

**Native SAML is deliberately not built.** The platform signs people in with OpenID Connect (see security.md).
For a customer whose identity provider only speaks SAML (ADFS, Shibboleth, older Okta or Azure setups), put an
OIDC bridge in front of it. This needs no internet and no change to the platform.

## Why not a SAML service provider inside the API

A SAML response is signed XML. Verifying it safely needs exclusive XML canonicalization, enveloped-signature
handling and strict protection against signature-wrapping and comment-injection attacks. Those flaws have
repeatedly broken well-known SAML libraries, and the platform's auth code is standard-library only by design
(air-gapped, small attack surface). Hand-rolling that parser to tick a feature box would put a login bypass
risk in the most sensitive code path. We chose not to, until a reviewed library can be vendored and audited.

## How to do it with a bridge

Run a self-hosted broker that accepts SAML from the customer's IdP and presents OIDC to HexThings, for example
Keycloak, Dex or Authentik, all of which run on-prem. Then:

1. In the broker, add the customer's IdP as a SAML identity provider (import its metadata file).
2. Create an OIDC client in the broker for HexThings with the redirect URI shown in Settings, SSO.
3. In HexThings, configure that client as the tenant's OIDC provider (issuer, client id, secret).
4. Map the broker's email claim to the HexThings user. First-login role is the lowest role until an admin changes
   it, as with any OIDC sign-in.

Sign-in then goes HexThings, broker, SAML IdP, and back. MFA policy of the IdP applies. HexThings's own local
sign-in MFA is separate (see security.md).

## Honest status

- Not built: SAML service provider, SAML single logout, SCIM provisioning.
- Documented, not tested here: the bridge setup above. No real Keycloak, Dex, Authentik or SAML IdP was run
  against the platform, so treat the steps as guidance to verify in a pilot.

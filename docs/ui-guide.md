# UI guide: where things are and how to do common tasks

This is the map the assistant uses for how-to answers. If a screen or setting is not listed here, it does not exist in the product.

## The left menu
Fleet, Devices, Assets, Customers (admin), Users (admin), KPIs, Map, Explorer, Add device, Scan, Dashboards, Assistant, Flows, Alerts, Control, Reports, Profiles, Audit, Settings. Customer-scoped users see only Devices, Alerts, Map, Dashboards and Reports. The assistant panel is the "Ask the assistant" button at the bottom right.

## How do I create a site?
There is no Sites page. A site is a physical location that devices and gateways belong to. Admins create one with **Create site** inside **Add device** or **Commission a sensor** (a name and an optional address), or by asking the assistant, which proposes it and waits for your confirmation. A site has a name and an address only. There is no site colour, no site logo, and sites cannot be renamed or deleted in the UI yet. A new workspace starts with a site called "Main site".

## How do I add a user?
Admins open **Users**. Use **Add a user** (email, name, role, optional initial password when local sign-in is on) or **Invite by link** (creates a one-time link, shown once, that the person opens to set their own password). Roles: admin, operator, installer, viewer, plus custom roles under **Custom roles**. Other buttons there: Set password, Reset authenticator, Disable. The assistant cannot add users or change roles; a person does that in Users.

## How do I create a customer?
Admins open **Customers**, **New customer** (name, optional parent). Then **Assign a device** to it and **Scope a user to a customer**: that user then sees only that customer's devices and alerts. A customer is an outside organisation. It is not an asset and not a site. The assistant can create a customer (with your confirmation) but scoping users is done in the Customers page.

## How do I create an asset?
**Assets**, **New asset** (name, kind: plant, line, machine, room or asset, optional parent), then **Attach a device**. An asset is part of the asset tree, for example a plant or a machine. It is not a customer. The assistant can create assets with your confirmation.

## How do I add a device?
**Add device**: pick a site (or Create site), a profile, enter the connection details, test the link and watch the first reading. Devices can be put in groups (the assistant can create a group) and tagged.

## What can the assistant change?
It proposes, you confirm with one click in the chat, and it runs as you with your role, audited. Always: create sites, assets, customers and device groups, and acknowledge or comment on alerts. With a hosted model it can also propose devices, dashboards, reports, flows, alert rules and maintenance windows; the small local model has only the first set. It can never approve control commands, and it cannot manage users, roles, invitations, API keys, secrets, SSO, feature switches, device credentials or its own settings. Those are done by a person in the normal UI.

## Things that do not exist
A site colour setting, a Settings > Sites page, a Terraform provider and CSV device import. Control commands are listed under Control. Settings holds notification channels, branding and the AI model, not users or sites.

**AI activity**: the Settings page has an AI activity card (admins only) listing what the assistant read and proposed. The assistant cannot list it for you; open Settings and scroll to it.

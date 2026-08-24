# Desk Management Specification

## Purpose

Defines the restrained responsive desks index added by Amendment 3 without changing existing desk CRUD, membership, or authorization behavior.

## Requirements

### Requirement: Responsive Desk Master/Detail Index

The desks index MUST present a simple master/detail management surface. The desk list MUST show each desk name and member count. Selecting a desk MUST reveal its detail with rename, a directly visible native destructive submit button labelled exactly `Delete desk`, add-member, and remove-member controls. `Delete desk` MUST use the existing POST route and server-side authorization, MUST preserve existing rejected-delete inline errors, and MUST NOT be hidden behind `More actions`, an overflow/disclosure, or a replacement client-side authority mechanism. Creating a desk MUST remain available through a disclosed new-desk form. Desktop may show list and detail together; narrow layouts MUST stack them without horizontal overflow and keep every action, including `Delete desk`, keyboard reachable with visible focus. The design MUST preserve existing tkt palette, typography, spacing, focus treatment, and simple user/admin philosophy; screenshot references may inform structure only and MUST NOT override those tokens.

#### Scenario: Admin manages the selected desk

- GIVEN an admin or root opens the desks index with multiple desks
- WHEN they select one desk
- THEN its detail shows the desk name, member list, and member-management controls
- AND the list continues to show member counts for every desk

#### Scenario: Direct desk delete remains server-authoritative

- GIVEN an admin or root views a deletable selected desk
- WHEN they activate the visible `Delete desk` submit button
- THEN the existing desk-delete POST route handles the request
- AND existing server-side authorization remains authoritative
- AND no `More actions` disclosure or client-side mutation authority is required

#### Scenario: Rejected direct delete remains inline

- GIVEN an authorized actor submits `Delete desk` for a desk the server rejects for deletion
- WHEN the existing POST route re-renders the management surface
- THEN the rejection appears inline in that surface
- AND the visible `Delete desk` control remains available according to the existing authorization and state rules

#### Scenario: New desk form stays disclosed until needed

- GIVEN an admin or root opens the desks index
- WHEN they have not chosen to create a desk
- THEN the new-desk form is not expanded
- WHEN they activate its disclosure control
- THEN the existing create form becomes available without changing the route contract

#### Scenario: Narrow desk management stacks without overflow

- GIVEN an admin or root views desks at 390px wide
- WHEN a selected desk has members and management controls
- THEN the list and detail stack in a readable order without horizontal scrolling
- AND every rename, delete, add-member, and remove-member control remains keyboard reachable with visible focus

### Requirement: Existing Desk Operations Remain Authoritative

The master/detail presentation MUST use the existing desk create, rename, delete, add-member, and remove-member routes and their server-side authorization. It MUST NOT introduce client-side authority, new desk roles, or alternate mutation endpoints.

#### Scenario: Existing authorization still gates a direct mutation

- GIVEN an actor without desk-management permission submits an existing desk mutation route directly
- WHEN the server handles the request
- THEN the request is denied by the existing server-side authorization
- AND no desk or membership write occurs

# Delta for Auth Entry Experience

## MODIFIED Requirements

### Requirement: Branded auth identity and content

Login MUST retain the established identity and form behavior while omitting the sentence “Use your work email and password.”
(Previously: login included that instructional sentence.)

#### Scenario: Obsolete copy absent
- GIVEN a user opens the login route
- WHEN the HTML is rendered
- THEN the obsolete sentence is absent and authentication controls remain available

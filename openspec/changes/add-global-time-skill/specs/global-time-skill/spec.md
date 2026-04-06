## ADDED Requirements

### Requirement: Agent can query current local time for a city or timezone
The system SHALL provide a global time skill that returns the current local time for a requested city alias or IANA timezone identifier.

#### Scenario: Query by IANA timezone
- **WHEN** the user asks for `time: Asia/Shanghai`
- **THEN** the system returns the current local time for `Asia/Shanghai`

#### Scenario: Query by city alias
- **WHEN** the user asks for `time: Tokyo`
- **THEN** the system resolves `Tokyo` to a supported timezone and returns the current local time

### Requirement: Planner can recognize time-query intents
The system SHALL map explicit and simple natural-language time requests to the global time skill.

#### Scenario: Explicit command prefix
- **WHEN** the user inputs `time: London`
- **THEN** the planner creates a tool-call step targeting the global time skill

#### Scenario: Natural language query
- **WHEN** the user asks “东京现在几点” or `time in London`
- **THEN** the planner creates a tool-call step targeting the global time skill with the inferred location

### Requirement: Time query output is chain-friendly
The system SHALL return a concise textual result that can be consumed by later tool steps in a multi-step workflow.

#### Scenario: Chained summary
- **WHEN** the user inputs `time: New York then summarize`
- **THEN** the first step returns a single concise time string
- **AND** the second step can consume that result without additional transformation

### Requirement: Unsupported locations fail clearly
The system SHALL return a clear error when the provided city or timezone cannot be resolved.

#### Scenario: Unknown location
- **WHEN** the user asks for `time: Atlantis`
- **THEN** the system returns an error indicating the location is unsupported or unknown

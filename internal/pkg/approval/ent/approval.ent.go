package ent

// Level is how much a tool call is allowed to happen on its own.
type Level int

const (
	Allow  Level = iota // runs without asking
	Ask                 // changes something: needs an approver's button
	Danger              // destroys data or can cut off access: button, shown in red
	Deny                // never runs
)

func (l Level) String() string {
	return [...]string{"allow", "ask", "danger", "deny"}[l]
}

// Decision is the policy's verdict on one tool call.
type Decision struct {
	Level  Level
	Reason string // why it is not Allow, for the approval message
}

// Worse returns whichever of the two decisions is stricter: a command line is
// as dangerous as its most dangerous part.
func Worse(a, b Decision) Decision {
	if b.Level > a.Level {
		return b
	}
	return a
}

// Verdict is what a person answered on an approval button.
type Verdict struct {
	OK bool
	By string // who pressed it, or why nobody did
}

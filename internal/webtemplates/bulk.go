package webtemplates

// BulkAction is one button in the shared bulk-actions bar (_bulk.html)
type BulkAction struct {
	Key     string
	Label   string
	Confirm string // question shown before running it
	Danger  bool
	Input   *BulkInput // asks for a value on confirm, e.g. a PHP version
}

// BulkInput describes the value field shown on confirm, Options turns it into a select and Fields into a pick-what-to-change select with one field per choice
type BulkInput struct {
	Type        string // "number", "text", "email", "password" or "select"
	Placeholder string
	Hint        string
	Min         string
	Max         string
	Step        string
	Pattern     string // checked in the browser and again on the server
	Default     string
	Options     []BulkOption
	Fields      []BulkField
}

// BulkField is one choice of a Fields input, e.g. the CPU limit of a plan
type BulkField struct {
	Key   string
	Label string
	Input BulkInput
}

type BulkOption struct {
	Value string
	Label string
}

// Usable is false for a select with nothing to pick
func (b BulkAction) Usable() bool {
	return b.Input == nil || b.Input.Type != "select" || len(b.Input.Options) > 0
}

// Initial is the value the field starts with: Default, or the first option of a select
func (i *BulkInput) Initial() string {
	if len(i.Fields) > 0 {
		return i.Fields[0].Input.Initial()
	}
	if i.Default == "" && len(i.Options) > 0 {
		return i.Options[0].Value
	}
	return i.Default
}

// Field looks up one of Fields by key
func (i *BulkInput) Field(key string) (BulkField, bool) {
	for _, f := range i.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return BulkField{}, false
}

// FieldInitials maps each of Fields to its starting value, the bar resets the value to it when another field is picked
func (i *BulkInput) FieldInitials() map[string]string {
	m := make(map[string]string, len(i.Fields))
	for _, f := range i.Fields {
		m[f.Key] = f.Input.Initial()
	}
	return m
}

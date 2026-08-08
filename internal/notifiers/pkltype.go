package notifiers

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// GeneratedSettingsPath is the repo-relative path of the generated Pkl module
// holding the contact-point settings types.
const GeneratedSettingsPath = "schema/pkl/alerting/generated/contact_point_settings.pkl"

// snapshotPath names the metadata snapshot the generated module is derived
// from, and refreshCommand the command that regenerates it. Both appear in the
// generated file's header.
const (
	snapshotPath   = "internal/notifiers/alert-notifiers.json"
	refreshCommand = "make generate"
)

// settingsClassName is the generated class carrying a contact point's
// top-level options.
const settingsClassName = "ContactPointSettings"

// The Pkl types the generator emits for leaf options.
const (
	// textType is the type of a free-text option. Widened with a Resolvable so
	// a value can be wired from another resource's property.
	textType = "(String|formae.Resolvable)?"
	// secretType additionally admits a formae.Value, which is not optional:
	// extract emits a hashed value as formae.value("<digest>").opaque.hashed,
	// and that only evaluates where the field admits formae.Value.
	secretType = "(String|formae.Value|formae.SecretValue|formae.Resolvable)?"
	// booleanType is deliberately not widened with a Resolvable: the resolver
	// substitutes a string, which Grafana rejects for a checkbox with a 400.
	booleanType    = "Boolean?"
	stringMapType  = "Mapping<String, String>?"
	stringListType = "Listing<String>?"
)

// conflictTypes resolves option names whose form element differs across
// notifier types in a way that yields two different Pkl types. Only "details"
// needs it: PagerDuty declares it a key/value map, Kafka a free-text field.
// An option whose element varies without changing its Pkl type - a title
// rendered as an input by one notifier and a textarea by another - is not a
// conflict and is absent here. An unlisted conflict is an error rather than an
// arbitrary choice between the shapes.
var conflictTypes = map[string]string{
	"details": "(String|Mapping<String, String>)?",
}

// pklKeywords are the words Pkl reserves. An option name that collides with
// one is emitted backtick-quoted, which serializes to the bare key.
var pklKeywords = map[string]struct{}{
	"abstract": {}, "amends": {}, "as": {}, "case": {}, "class": {}, "const": {},
	"delete": {}, "else": {}, "extends": {}, "external": {}, "false": {},
	"fixed": {}, "for": {}, "function": {}, "hidden": {}, "if": {}, "import": {},
	"in": {}, "is": {}, "let": {}, "local": {}, "module": {}, "new": {},
	"nothing": {}, "null": {}, "open": {}, "out": {}, "outer": {},
	"override": {}, "protected": {}, "read": {}, "record": {}, "super": {},
	"switch": {}, "this": {}, "throw": {}, "trace": {}, "true": {},
	"typealias": {}, "vararg": {}, "when": {},
}

// pklProperty is one property of a generated class: the Grafana option name,
// emitted verbatim so it serializes to Grafana's own key, and its Pkl type.
type pklProperty struct {
	name string
	typ  string
}

// pklClass is one generated Pkl class. sources names the options the class was
// derived from - more than one when distinct option names share a field shape.
// deps names the generated classes the class references, so classes can be
// emitted nested-first.
type pklClass struct {
	name    string
	sources []string
	props   []pklProperty
	deps    []string
}

// RenderContactPointSettings renders the generated Pkl module declaring the
// contact-point settings types for the notifier vocabulary ns.
func RenderContactPointSettings(ns []Notifier) (string, error) {
	classes, err := settingsClasses(ns)
	if err != nil {
		return "", err
	}
	return renderClasses(classes), nil
}

// settingsClasses derives the generated classes from ns: one class per
// distinct subform shape, in dependency order, followed by the top-level
// settings class.
func settingsClasses(ns []Notifier) ([]pklClass, error) {
	secrets := SecretNames(ns)

	shapes := newOptionShapes()
	for _, n := range ns {
		if err := shapes.collect(n.Options, secrets); err != nil {
			return nil, err
		}
	}

	types, err := shapes.types(secrets)
	if err != nil {
		return nil, err
	}

	subformClasses, err := shapes.subformClasses(types)
	if err != nil {
		return nil, err
	}

	classes, err := orderClasses(subformClasses)
	if err != nil {
		return nil, err
	}

	settings := pklClass{name: settingsClassName}
	for _, name := range topLevelNames(ns) {
		settings.props = append(settings.props, pklProperty{name: name, typ: types[name]})
	}
	return append(classes, settings), nil
}

// optionShapes indexes every option name in a notifier vocabulary by the form
// elements it is declared with and, for subform options, by its field list.
type optionShapes struct {
	elements map[string]map[string]struct{}
	subforms map[string][]Field
}

func newOptionShapes() *optionShapes {
	return &optionShapes{
		elements: make(map[string]map[string]struct{}),
		subforms: make(map[string][]Field),
	}
}

// collect walks a field tree, recording each option's element and, for a
// subform option, its field list. It rejects a subform whose field shape
// disagrees with an earlier occurrence of the same option name, and a subform
// option marked secure, neither of which the generator can type.
func (s *optionShapes) collect(fields []Field, secrets map[string]struct{}) error {
	for _, f := range fields {
		if s.elements[f.PropertyName] == nil {
			s.elements[f.PropertyName] = make(map[string]struct{})
		}
		s.elements[f.PropertyName][f.Element] = struct{}{}

		if len(f.SubformOptions) == 0 {
			continue
		}
		if _, secret := secrets[f.PropertyName]; secret {
			return fmt.Errorf("notifiers: subform option %q is marked secure, which has no generated typing", f.PropertyName)
		}
		if prior, seen := s.subforms[f.PropertyName]; seen {
			if shapeKey(prior) != shapeKey(f.SubformOptions) {
				return fmt.Errorf("notifiers: subform option %q has more than one field shape", f.PropertyName)
			}
		} else {
			s.subforms[f.PropertyName] = f.SubformOptions
		}
		if err := s.collect(f.SubformOptions, secrets); err != nil {
			return err
		}
	}
	return nil
}

// types resolves every collected option name to its Pkl type.
func (s *optionShapes) types(secrets map[string]struct{}) (map[string]string, error) {
	types := make(map[string]string, len(s.elements))
	for _, name := range slices.Sorted(maps.Keys(s.elements)) {
		_, secret := secrets[name]

		elements := slices.Sorted(maps.Keys(s.elements[name]))
		var candidates []string
		for _, element := range elements {
			typ, err := optionType(name, element, secret)
			if err != nil {
				return nil, err
			}
			if !slices.Contains(candidates, typ) {
				candidates = append(candidates, typ)
			}
		}

		if len(candidates) == 1 {
			types[name] = candidates[0]
			continue
		}
		typ, resolved := conflictTypes[name]
		if !resolved {
			return nil, fmt.Errorf("notifiers: option %q is declared with elements %v, which map to different Pkl types", name, elements)
		}
		types[name] = typ
	}
	return types, nil
}

// subformClasses builds one class per distinct subform shape, keyed by class
// name so option names that upper-camel-case to the same name share a class.
func (s *optionShapes) subformClasses(types map[string]string) (map[string]pklClass, error) {
	classes := make(map[string]pklClass)
	for _, name := range slices.Sorted(maps.Keys(s.subforms)) {
		cls := pklClass{name: pklClassName(name), sources: []string{name}}
		for _, f := range s.subforms[name] {
			cls.props = append(cls.props, pklProperty{name: f.PropertyName, typ: types[f.PropertyName]})
			if len(f.SubformOptions) > 0 {
				cls.deps = append(cls.deps, pklClassName(f.PropertyName))
			}
		}
		slices.SortFunc(cls.props, func(a, b pklProperty) int { return strings.Compare(a.name, b.name) })
		slices.Sort(cls.deps)

		prior, seen := classes[cls.name]
		if !seen {
			classes[cls.name] = cls
			continue
		}
		if propsKey(prior.props) != propsKey(cls.props) {
			return nil, fmt.Errorf("notifiers: options %v and %q both generate class %s but declare different fields", prior.sources, name, cls.name)
		}
		prior.sources = append(prior.sources, name)
		slices.Sort(prior.sources)
		classes[cls.name] = prior
	}
	return classes, nil
}

// optionType returns the Pkl type for an option declared with the given form
// element. A secret-classified name is typed to admit a formae.SecretValue
// regardless of the element it is rendered with.
func optionType(name, element string, secret bool) (string, error) {
	switch element {
	case "input", "select", "textarea":
		if secret {
			return secretType, nil
		}
		return textType, nil
	case "checkbox":
		return booleanType, nil
	case "key_value_map":
		return stringMapType, nil
	case "string_array":
		return stringListType, nil
	case "subform":
		return pklClassName(name) + "?", nil
	case "subform_array":
		return "Listing<" + pklClassName(name) + ">?", nil
	default:
		return "", fmt.Errorf("notifiers: option %q has unsupported element %q", name, element)
	}
}

// orderClasses returns the classes nested-first, so a class is declared before
// the classes referencing it. Among the classes whose dependencies are already
// declared it always takes the alphabetically first, making the order a
// function of the input alone.
func orderClasses(classes map[string]pklClass) ([]pklClass, error) {
	names := slices.Sorted(maps.Keys(classes))
	declared := make(map[string]bool, len(classes))
	ordered := make([]pklClass, 0, len(classes))

	for len(ordered) < len(classes) {
		progressed := false
		for _, name := range names {
			if declared[name] || !dependenciesDeclared(classes[name], declared) {
				continue
			}
			ordered = append(ordered, classes[name])
			declared[name] = true
			progressed = true
			break
		}
		if !progressed {
			return nil, fmt.Errorf("notifiers: subform classes reference each other cyclically")
		}
	}
	return ordered, nil
}

func dependenciesDeclared(cls pklClass, declared map[string]bool) bool {
	for _, dep := range cls.deps {
		if !declared[dep] {
			return false
		}
	}
	return true
}

// topLevelNames returns every option name declared at the top level of any
// notifier type, sorted.
func topLevelNames(ns []Notifier) []string {
	names := make(map[string]struct{})
	for _, n := range ns {
		for _, f := range n.Options {
			names[f.PropertyName] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(names))
}

// renderClasses renders the generated module: the header, the formae import,
// then each class in the order given.
func renderClasses(classes []pklClass) string {
	var b strings.Builder

	b.WriteString("/// Typed settings blocks for a Grafana contact point, one property per\n")
	b.WriteString("/// option the notifier metadata declares.\n")
	b.WriteString("///\n")
	fmt.Fprintf(&b, "/// Generated from `%s` - DO NOT EDIT.\n", snapshotPath)
	fmt.Fprintf(&b, "/// Refresh with `%s`.\n", refreshCommand)
	b.WriteString("module alerting.generated.contact_point_settings\n")
	b.WriteString("\n")
	b.WriteString("import \"@formae/formae.pkl\"\n")

	for _, cls := range classes {
		b.WriteString("\n")
		b.WriteString(classDoc(cls))
		// Rendering a sub-resource's properties reads the key transformation off
		// its SubResourceHint, so a class without one cannot be rendered at all.
		b.WriteString("@formae.SubResourceHint {}\n")
		fmt.Fprintf(&b, "open class %s extends formae.SubResource {\n", cls.name)
		for _, p := range cls.props {
			fmt.Fprintf(&b, "  %s: %s\n", pklPropertyName(p.name), p.typ)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// classDoc renders a class's doc comment. The top-level settings class gets a
// prose description; a subform class names the options it was derived from.
func classDoc(cls pklClass) string {
	if cls.name == settingsClassName {
		return "/// A contact point's type-specific settings. Every option any notifier\n" +
			"/// type declares is present as an optional property, so a key Grafana does\n" +
			"/// not accept for the declared type is a Pkl type error rather than a\n" +
			"/// setting silently dropped on submission.\n"
	}
	quoted := make([]string, 0, len(cls.sources))
	for _, source := range cls.sources {
		quoted = append(quoted, "`"+source+"`")
	}
	if len(quoted) == 1 {
		return fmt.Sprintf("/// Nested settings block declared by the %s option.\n", quoted[0])
	}
	last := len(quoted) - 1
	return fmt.Sprintf("/// Nested settings block declared by the %s and %s options.\n",
		strings.Join(quoted[:last], ", "), quoted[last])
}

// pklClassName upper-camel-cases an option name into the name of the class
// generated for its subform. "tlsConfig" and "tls_config" both yield
// "TlsConfig", which is why those two options share one class.
func pklClassName(name string) string {
	var b strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// pklPropertyName backtick-quotes an option name that collides with a Pkl
// keyword, leaving every other name verbatim.
func pklPropertyName(name string) string {
	if _, reserved := pklKeywords[name]; reserved {
		return "`" + name + "`"
	}
	return name
}

// shapeKey canonicalizes a subform's field list so two occurrences can be
// compared regardless of the order Grafana declares them in. Nested shapes are
// keyed by option name and validated on their own, so one level suffices.
func shapeKey(fields []Field) string {
	pairs := make([]string, 0, len(fields))
	for _, f := range fields {
		pairs = append(pairs, f.PropertyName+":"+f.Element)
	}
	slices.Sort(pairs)
	return strings.Join(pairs, ",")
}

// propsKey canonicalizes a generated class's properties for comparison.
func propsKey(props []pklProperty) string {
	pairs := make([]string, 0, len(props))
	for _, p := range props {
		pairs = append(pairs, p.name+":"+p.typ)
	}
	return strings.Join(pairs, ",")
}

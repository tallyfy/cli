package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tallyfy/cli/pkg/tallyfy"
)

// The template create route (POST /organizations/{org}/checklists) ignores
// "steps" and "stages": api-v2's Checklist::build keeps only its fillable
// template fields. So `blueprint import` creates the template first, then its
// milestones, then each step in order, through the routes that do accept them.

// blueprintStepStripFields are server-managed step fields removed before a
// step is re-created. checklist_id is replaced by the new blueprint's id,
// stage_id is re-linked through link-stage, and position follows from the
// creation order (the API puts each new step last).
var blueprintStepStripFields = []string{
	"id", "increment_id", "checklist_id", "alias", "position", "stage_id",
	"created_at", "last_updated", "archived_at", "owner_id", "owner",
}

// blueprintCaptureStripFields are server-managed form field (capture) fields.
// The id matters most: api-v2 reads a capture id as "update this existing
// field" and skips any id that is not already on the step being saved
// (CaptureRepository::sync), so sending the exported ids would drop every
// form field from the new step.
var blueprintCaptureStripFields = []string{
	"id", "increment_id", "step_id", "alias", "created_at", "last_updated",
}

// deadlineStartRun is the deadline anchor meaning "from process start".
const deadlineStartRun = "start_run"

type importMilestone struct {
	oldKey   string
	title    string
	position int
}

type importStep struct {
	oldID    string
	title    string
	body     map[string]json.RawMessage
	deadline map[string]json.RawMessage // nil when the export has none
	anchor   string                     // old step id the deadline counts from; "" for process start
	stageKey string                     // old milestone id; "" when the step has none
}

type importPlan struct {
	template    map[string]json.RawMessage
	stripped    []string
	milestones  []importMilestone
	steps       []importStep
	automations int    // automated actions in the file, which the import does not re-create
	sourceOrg   string // the org the file was exported from; "" when the file does not say
}

// importIncompleteError reports an import that created the blueprint but
// stopped part way. It wraps the API error so the exit code still follows it.
type importIncompleteError struct {
	blueprintID string
	stage       string
	err         error
}

func (e *importIncompleteError) Error() string {
	return fmt.Sprintf("blueprint %s was created, but the import stopped at %s: %v; the blueprint is incomplete, so delete it (tallyfy blueprint delete %s) and import again",
		e.blueprintID, e.stage, e.err, e.blueprintID)
}

func (e *importIncompleteError) Unwrap() error { return e.err }

// parseImportPlan turns an exported blueprint into the template create body
// plus the milestones and steps to create after it. Anything malformed is a
// usage error, found before a single request is sent.
func parseImportPlan(data []byte) (*importPlan, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, &UsageError{Msg: "import file must be a JSON object: " + err.Error()}
	}
	plan := &importPlan{}

	stepItems, err := includeItems(obj["steps"])
	if err != nil {
		return nil, &UsageError{Msg: "import file: steps: " + err.Error()}
	}
	stageItems, err := includeItems(obj["stages"])
	if err != nil {
		return nil, &UsageError{Msg: "import file: stages: " + err.Error()}
	}
	delete(obj, "steps")
	delete(obj, "stages")

	for _, f := range blueprintImportStripFields {
		if _, ok := obj[f]; ok {
			delete(obj, f)
			plan.stripped = append(plan.stripped, f)
		}
	}
	if err := stripKickoffIDs(obj); err != nil {
		return nil, err
	}
	if raw, ok := obj[exportOrgField]; ok {
		_ = json.Unmarshal(raw, &plan.sourceOrg)
		delete(obj, exportOrgField)
	}
	// The template create ignores automated_actions too. Re-creating them
	// needs every step, form field and kick-off field id they name mapped to
	// the new copies, which the import does not do yet, so it says so.
	var automations []json.RawMessage
	if json.Unmarshal(obj["automated_actions"], &automations) == nil {
		plan.automations = len(automations)
	}
	plan.template = obj

	for i, raw := range stageItems {
		m, err := parseImportMilestone(raw, i)
		if err != nil {
			return nil, err
		}
		plan.milestones = append(plan.milestones, m)
	}
	sort.SliceStable(plan.milestones, func(a, b int) bool {
		return plan.milestones[a].position < plan.milestones[b].position
	})

	positions := make([]float64, len(stepItems))
	allPositioned := true
	for i, raw := range stepItems {
		s, pos, ok, err := parseImportStep(raw, i)
		if err != nil {
			return nil, err
		}
		plan.steps = append(plan.steps, s)
		positions[i] = pos
		allPositioned = allPositioned && ok
	}
	if allPositioned {
		idx := make([]int, len(plan.steps))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return positions[idx[a]] < positions[idx[b]] })
		ordered := make([]importStep, len(plan.steps))
		for i, j := range idx {
			ordered[i] = plan.steps[j]
		}
		plan.steps = ordered
	}
	return plan, nil
}

// stripKickoffIDs removes the id from each kick-off field ("prerun"). The
// template create keeps a kick-off field only when it carries no id: with the
// exported id it is dropped and the import still answers 201 (measured on
// staging, tallyfy/work-queue#4261). The server sets the new field's
// template link and alias itself.
func stripKickoffIDs(obj map[string]json.RawMessage) error {
	raw, ok := obj["prerun"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return &UsageError{Msg: "import file: prerun must be an array of kick-off field objects"}
	}
	for _, f := range fields {
		delete(f, "id")
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	obj["prerun"] = b
	return nil
}

// includeItems reads a relationship as exported: the API wraps an include as
// {"data": [...]}, and a hand-written file may use a bare array.
func includeItems(raw json.RawMessage) ([]json.RawMessage, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if t[0] == '[' {
		if err := json.Unmarshal(t, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	var wrapped struct {
		Data *[]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(t, &wrapped); err != nil || wrapped.Data == nil {
		return nil, fmt.Errorf("want an array or {\"data\": [...]}")
	}
	return *wrapped.Data, nil
}

func parseImportMilestone(raw json.RawMessage, i int) (importMilestone, error) {
	var m struct {
		ID       json.RawMessage `json:"id"`
		Title    string          `json:"title"`
		Position *float64        `json:"position"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return importMilestone{}, &UsageError{Msg: fmt.Sprintf("import file: milestone %d is not an object: %v", i+1, err)}
	}
	if strings.TrimSpace(m.Title) == "" {
		return importMilestone{}, &UsageError{Msg: fmt.Sprintf("import file: milestone %d has no title", i+1)}
	}
	pos := i + 1
	if m.Position != nil {
		pos = int(*m.Position)
	}
	return importMilestone{oldKey: idKey(m.ID), title: m.Title, position: pos}, nil
}

// parseImportStep builds one step's create body. It reports the step's
// exported position (ok=false when it has none).
func parseImportStep(raw json.RawMessage, i int) (s importStep, pos float64, ok bool, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return s, 0, false, &UsageError{Msg: fmt.Sprintf("import file: step %d is not an object", i+1)}
	}
	_ = json.Unmarshal(obj["title"], &s.title)
	if strings.TrimSpace(s.title) == "" {
		return s, 0, false, &UsageError{Msg: fmt.Sprintf("import file: step %d has no title", i+1)}
	}
	_ = json.Unmarshal(obj["id"], &s.oldID)
	s.stageKey = idKey(obj["stage_id"])
	ok = json.Unmarshal(obj["position"], &pos) == nil && len(obj["position"]) > 0 && string(obj["position"]) != "null"

	for _, f := range blueprintStepStripFields {
		delete(obj, f)
	}
	dropNulls(obj)

	if rawCaps, has := obj["captures"]; has {
		var caps []map[string]json.RawMessage
		if err := json.Unmarshal(rawCaps, &caps); err != nil {
			return s, 0, false, &UsageError{Msg: fmt.Sprintf("import file: step %d (%q): captures must be an array of objects", i+1, s.title)}
		}
		for _, c := range caps {
			for _, f := range blueprintCaptureStripFields {
				delete(c, f)
			}
			dropNulls(c)
		}
		b, err := json.Marshal(caps)
		if err != nil {
			return s, 0, false, err
		}
		obj["captures"] = b
	}

	if rawDL, has := obj["deadline"]; has {
		if err := json.Unmarshal(rawDL, &s.deadline); err != nil || s.deadline == nil {
			return s, 0, false, &UsageError{Msg: fmt.Sprintf("import file: step %d (%q): deadline must be an object", i+1, s.title)}
		}
		var anchor string
		_ = json.Unmarshal(s.deadline["step"], &anchor)
		if anchor != deadlineStartRun {
			s.anchor = anchor
		}
		delete(obj, "deadline")
	}

	s.body = obj
	return s, pos, ok, nil
}

// dropNulls removes keys whose value is JSON null. An exported null means
// "not set", and api-v2 rejects a present null for its typed step fields
// (boolean, integer, array) that are not marked nullable.
func dropNulls(m map[string]json.RawMessage) {
	for k, v := range m {
		if string(bytes.TrimSpace(v)) == "null" {
			delete(m, k)
		}
	}
}

// idKey normalizes an id that may arrive as a JSON number (a step's stage_id)
// or a JSON string (a milestone's id) to one comparable form.
func idKey(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(t, &s) == nil {
		return s
	}
	return string(t)
}

// importResult counts what an import created.
type importResult struct {
	blueprint  *tallyfy.Blueprint
	raw        json.RawMessage
	steps      int
	milestones int
}

// runBlueprintImport creates the template, then its milestones, then each
// step in order, links steps to their milestones, and finally sets any
// deadline that counts from a step later in the file. Step deadlines that
// count from another step are re-pointed at the new step, never left pointing
// at the source template's step.
func runBlueprintImport(cctx context.Context, ctx *Context, org string, plan *importPlan) (*importResult, error) {
	// Done before anything is created, so a failed lookup leaves nothing behind.
	if c := plan.checks(org); c.needed() {
		t, err := readTargetOrg(cctx, ctx, org, c)
		if err != nil {
			return nil, fmt.Errorf("check the %s against org %s before creating anything: %w", c.describe(), org, err)
		}
		leaveOffRefused(ctx, plan, org, t)
	}

	payload, err := json.Marshal(plan.template)
	if err != nil {
		return nil, err
	}
	b, raw, err := ctx.API.CreateBlueprint(cctx, org, payload)
	if err != nil {
		return nil, err
	}
	res := &importResult{blueprint: b, raw: raw}
	fail := func(stage string, err error) (*importResult, error) {
		return res, &importIncompleteError{blueprintID: b.ID, stage: stage, err: err}
	}

	newMilestone := map[string]json.Number{}
	for i, m := range plan.milestones {
		data, err := ctx.API.CreateBlueprintMilestone(cctx, org, b.ID, m.title, m.position)
		if err != nil {
			return fail(fmt.Sprintf("milestone %d of %d (%q)", i+1, len(plan.milestones), m.title), err)
		}
		var created struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(data, &created)
		id := idKey(created.ID)
		if !isNumeric(id) {
			return fail(fmt.Sprintf("milestone %d of %d (%q)", i+1, len(plan.milestones), m.title),
				fmt.Errorf("unexpected milestone id %s in the API response", string(created.ID)))
		}
		if m.oldKey != "" {
			newMilestone[m.oldKey] = json.Number(id)
		}
		res.milestones++
	}

	inFile := map[string]bool{}
	for _, s := range plan.steps {
		if s.oldID != "" {
			inFile[s.oldID] = true
		}
	}
	newStep := map[string]string{}
	createdIDs := make([]string, len(plan.steps))
	var deferred []int
	warnedMilestone := map[string]bool{}

	for i, s := range plan.steps {
		where := fmt.Sprintf("step %d of %d (%q)", i+1, len(plan.steps), s.title)
		body := make(map[string]json.RawMessage, len(s.body)+2)
		for k, v := range s.body {
			body[k] = v
		}
		body["checklist_id"], _ = json.Marshal(b.ID)

		if s.deadline != nil {
			switch {
			case s.anchor == "":
				body["deadline"], _ = json.Marshal(s.deadline)
			case newStep[s.anchor] != "":
				body["deadline"] = repointDeadline(s.deadline, newStep[s.anchor])
			case inFile[s.anchor]:
				deferred = append(deferred, i) // counts from a later step: set once that step exists
			default:
				ctx.Infof("warning: %s: its deadline counts from step %s, which is not in the file, so it gets the default deadline\n", where, s.anchor)
			}
		}

		payload, err := json.Marshal(body)
		if err != nil {
			return fail(where, err)
		}
		data, err := ctx.API.CreateBlueprintStep(cctx, org, b.ID, payload)
		if err != nil {
			return fail(where, err)
		}
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &created)
		if created.ID == "" {
			return fail(where, fmt.Errorf("the API response has no step id"))
		}
		if s.oldID != "" {
			newStep[s.oldID] = created.ID
		}
		createdIDs[i] = created.ID
		res.steps++

		if s.stageKey != "" {
			stageID, ok := newMilestone[s.stageKey]
			if !ok {
				if !warnedMilestone[s.stageKey] {
					ctx.Infof("warning: milestone %s is not in the file, so its steps were imported without a milestone (export again to include milestones)\n", s.stageKey)
					warnedMilestone[s.stageKey] = true
				}
				continue
			}
			if _, err := ctx.API.LinkStepMilestone(cctx, org, b.ID, created.ID, stageID); err != nil {
				return fail(where+" milestone link", err)
			}
		}
	}

	for _, i := range deferred {
		s := plan.steps[i]
		where := fmt.Sprintf("the deadline of step %d of %d (%q)", i+1, len(plan.steps), s.title)
		body := map[string]json.RawMessage{"deadline": repointDeadline(s.deadline, newStep[s.anchor])}
		body["title"], _ = json.Marshal(s.title)
		payload, err := json.Marshal(body)
		if err != nil {
			return fail(where, err)
		}
		if _, err := ctx.API.UpdateBlueprintStep(cctx, org, b.ID, createdIDs[i], payload); err != nil {
			return fail(where, err)
		}
	}
	return res, nil
}

// repointDeadline copies a deadline with its anchor step replaced.
func repointDeadline(deadline map[string]json.RawMessage, stepID string) json.RawMessage {
	out := make(map[string]json.RawMessage, len(deadline))
	for k, v := range deadline {
		out[k] = v
	}
	out["step"], _ = json.Marshal(stepID)
	b, _ := json.Marshal(out)
	return b
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// exportOrgField is the key `blueprint export` adds to record the org a file
// came from, so an import can tell a same-org import from a cross-org one.
const exportOrgField = "exported_from_org"

// withExportOrg adds exportOrgField as the first key of an exported
// blueprint, keeping the API's own key order for everything after it.
func withExportOrg(raw json.RawMessage, org string) json.RawMessage {
	t := bytes.TrimSpace(raw)
	if len(t) < 2 || t[0] != '{' {
		return raw
	}
	key, _ := json.Marshal(exportOrgField)
	val, _ := json.Marshal(org)
	rest := bytes.TrimSpace(t[1:])
	var b bytes.Buffer
	b.WriteByte('{')
	b.Write(key)
	b.WriteByte(':')
	b.Write(val)
	if len(rest) > 0 && rest[0] != '}' {
		b.WriteByte(',')
	}
	b.Write(rest)
	if !json.Valid(b.Bytes()) {
		return raw
	}
	return b.Bytes()
}

// importChecks says what an import must check against the org it imports
// into before it creates anything. api-v2 answers 422 to a blueprint or step
// naming a user or group the org does not have (org_user, org_group), to a
// default tag the org does not have (CreateChecklistRequest), and to a step
// guest the org would not take as a guest (ValidGuestEmail). Into the org the
// file was exported from, everything is kept as exported and nothing is
// checked (owner decision, tallyfy/work-queue#4280). A file that does not say
// where it came from is checked.
type importChecks struct {
	users  bool // the blueprint or a step names users
	groups bool // the blueprint or a step names groups
	tag    bool // the blueprint has a default tag
	guests bool // a step has guests
}

func (c importChecks) needed() bool { return c.users || c.groups || c.tag || c.guests }

// describe names what will be checked, for the dry run.
func (c importChecks) describe() string {
	var parts []string
	switch {
	case c.users && c.groups:
		parts = append(parts, "assigned users and groups")
	case c.users:
		parts = append(parts, "assigned users")
	case c.groups:
		parts = append(parts, "assigned groups")
	}
	if c.tag {
		parts = append(parts, "default tag")
	}
	if c.guests {
		parts = append(parts, "step guests")
	}
	if len(parts) < 2 {
		return strings.Join(parts, "")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func (p *importPlan) checks(org string) importChecks {
	var c importChecks
	if p.sourceOrg != "" && p.sourceOrg == org {
		return c
	}
	c.users = len(stepIDs(p.template["users"])) > 0
	c.groups = len(stepIDs(p.template["groups"])) > 0
	c.tag = idKey(p.template["default_tag"]) != ""
	for _, s := range p.steps {
		c.users = c.users || len(stepIDs(s.body["assignees"])) > 0
		c.groups = c.groups || len(stepIDs(s.body["groups"])) > 0
		c.guests = c.guests || len(guestEmails(s.body["guests"])) > 0
	}
	return c
}

func (p *importPlan) sourceDescription() string {
	if p.sourceOrg == "" {
		return "the file does not say which org it was exported from"
	}
	return "the file was exported from org " + p.sourceOrg
}

// stepIDs reads a list of user ids (numbers) or group ids (strings),
// normalized to strings.
func stepIDs(raw json.RawMessage) []string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		if k := idKey(it); k != "" {
			ids = append(ids, k)
		}
	}
	return ids
}

// guestEmails reads a step's guests, an array of email addresses.
func guestEmails(raw json.RawMessage) []string {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	emails := make([]string, 0, len(items))
	for _, it := range items {
		var e string
		if json.Unmarshal(it, &e) == nil && e != "" {
			emails = append(emails, e)
		}
	}
	return emails
}

// targetOrg is what the import read from the org it imports into. A set is
// nil when the file gave no reason to read it.
type targetOrg struct {
	users      map[string]bool // member user ids
	emails     map[string]bool // member email addresses, as the members list gives them
	groups     map[string]bool // group ids
	tags       map[string]bool // tag ids
	guests     bool            // step guests are checked
	ssoDomains map[string]bool // company domains, set only when single sign-on is on
}

// readTargetOrg reads only what the checks need. Each list holds what the
// matching api-v2 rule accepts: org_user and the members list both read the
// org's membership rows that were not removed, org_group and the groups list
// the org's groups, and the default_tag rule any tag of the org.
func readTargetOrg(cctx context.Context, ctx *Context, org string, c importChecks) (*targetOrg, error) {
	t := &targetOrg{}
	if c.users || c.guests {
		us, _, err := ctx.API.ListUsers(cctx, org, tallyfy.ListOptions{All: true})
		if err != nil {
			return nil, err
		}
		t.users = make(map[string]bool, len(us))
		t.emails = make(map[string]bool, len(us))
		for _, u := range us {
			t.users[u.ID.String()] = true
			if u.Email != "" {
				t.emails[u.Email] = true
			}
		}
	}
	if c.groups {
		gs, _, err := ctx.API.ListGroups(cctx, org, tallyfy.ListOptions{All: true})
		if err != nil {
			return nil, err
		}
		t.groups = make(map[string]bool, len(gs))
		for _, g := range gs {
			t.groups[g.ID] = true
		}
	}
	if c.tag {
		// Without all=true the tags list leaves out the tags api-v2 makes for
		// processes (titles starting "from:", "task:" or "step:"), and the
		// default_tag rule accepts those too.
		ts, _, err := ctx.API.ListTags(cctx, org, tallyfy.ListOptions{All: true, Extra: map[string]string{"all": "true"}})
		if err != nil {
			return nil, err
		}
		t.tags = make(map[string]bool, len(ts))
		for _, tg := range ts {
			t.tags[tg.ID] = true
		}
	}
	if c.guests {
		t.guests = true
		_, raw, err := ctx.API.GetOrganization(cctx, org)
		if err != nil {
			return nil, err
		}
		var o struct {
			SamlEnabled bool `json:"saml_enabled"`
		}
		if err := json.Unmarshal(raw, &o); err != nil {
			return nil, fmt.Errorf("read org %s: %w", org, err)
		}
		if o.SamlEnabled {
			ds, err := ctx.API.ListDomains(cctx, org)
			if err != nil {
				return nil, err
			}
			t.ssoDomains = make(map[string]bool, len(ds))
			for _, d := range ds {
				t.ssoDomains[d.Name] = true
			}
		}
	}
	return t, nil
}

// guestRefusal says why the org would refuse this guest email on a step, or
// "" when it would take it. It follows api-v2's ValidGuestEmail, in its
// order: the email is lowercased, then refused when single sign-on is on and
// the part after the "@" is one of the org's company domains, or when it is
// a member's email address.
func (t *targetOrg) guestRefusal(email string) string {
	e := strings.ToLower(email)
	if t.ssoDomains != nil {
		if _, domain, ok := strings.Cut(e, "@"); ok && t.ssoDomains[domain] {
			return "single sign-on domain " + domain
		}
	}
	if t.emails[e] {
		return "already a member"
	}
	return ""
}

// leaveOffRefused removes from the plan what the target org would refuse,
// and prints one warning for each kind it left off.
func leaveOffRefused(ctx *Context, plan *importPlan, org string, t *targetOrg) {
	if dropped := dropForeignAssignees(plan, t.users, t.groups); len(dropped) > 0 {
		ctx.Infof("warning: %s, so the import leaves off %d assignee(s) that are not in org %s: %s\n",
			plan.sourceDescription(), len(dropped), org, strings.Join(dropped, ", "))
	}
	if tag := dropMissingDefaultTag(plan, t.tags); tag != "" {
		ctx.Infof("warning: %s, so the import leaves off the default tag %s, which is not in org %s\n",
			plan.sourceDescription(), tag, org)
	}
	if dropped := dropRefusedGuests(plan, t); len(dropped) > 0 {
		ctx.Infof("warning: %s, so the import leaves off %d guest(s) that org %s would refuse: %s\n",
			plan.sourceDescription(), len(dropped), org, strings.Join(dropped, ", "))
	}
}

// dropForeignAssignees removes the users and groups that are not in the
// target org, from the blueprint itself and from every step, and names each
// one it removed. A nil set means it was not read, so nothing is removed.
func dropForeignAssignees(plan *importPlan, users, groups map[string]bool) []string {
	var dropped []string
	type list struct {
		key, kind string
		members   map[string]bool
	}
	keep := func(body map[string]json.RawMessage, where string, lists []list) {
		for _, f := range lists {
			raw, ok := body[f.key]
			if !ok || f.members == nil {
				continue
			}
			var items []json.RawMessage
			if json.Unmarshal(raw, &items) != nil {
				continue
			}
			kept := make([]json.RawMessage, 0, len(items))
			for _, it := range items {
				if id := idKey(it); id != "" && !f.members[id] {
					dropped = append(dropped, fmt.Sprintf("%s %s on %s", f.kind, id, where))
					continue
				}
				kept = append(kept, it)
			}
			if len(kept) != len(items) {
				b, _ := json.Marshal(kept)
				body[f.key] = b
			}
		}
	}
	keep(plan.template, "the blueprint", []list{{"users", "user", users}, {"groups", "group", groups}})
	for i := range plan.steps {
		s := &plan.steps[i]
		keep(s.body, fmt.Sprintf("step %d (%q)", i+1, s.title), []list{{"assignees", "user", users}, {"groups", "group", groups}})
	}
	return dropped
}

// dropMissingDefaultTag removes the blueprint's default tag when the target
// org does not have it, and returns the tag id it removed.
func dropMissingDefaultTag(plan *importPlan, tags map[string]bool) string {
	id := idKey(plan.template["default_tag"])
	if id == "" || tags == nil || tags[id] {
		return ""
	}
	delete(plan.template, "default_tag")
	return id
}

// dropRefusedGuests removes from every step the guests the target org would
// refuse, and names each one with the reason.
func dropRefusedGuests(plan *importPlan, t *targetOrg) []string {
	if !t.guests {
		return nil
	}
	var dropped []string
	for i := range plan.steps {
		s := &plan.steps[i]
		raw, ok := s.body["guests"]
		if !ok {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			continue
		}
		kept := make([]json.RawMessage, 0, len(items))
		for _, it := range items {
			var email string
			if json.Unmarshal(it, &email) == nil && email != "" {
				if why := t.guestRefusal(email); why != "" {
					dropped = append(dropped, fmt.Sprintf("guest %s on step %d (%q) (%s)", email, i+1, s.title, why))
					continue
				}
			}
			kept = append(kept, it)
		}
		if len(kept) != len(items) {
			b, _ := json.Marshal(kept)
			s.body["guests"] = b
		}
	}
	return dropped
}

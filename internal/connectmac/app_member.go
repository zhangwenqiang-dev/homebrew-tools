package connectmac

import (
	"fmt"
	"strings"
)

func (a App) runMember(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "usage: cm member <list|add|enable|disable|assign|unassign> ...")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(a.Err, "usage: cm member list")
			return 2
		}
		members, err := a.MemberStore.ListMembers()
		if err != nil {
			fmt.Fprintf(a.Err, "member list failed: %v\n", err)
			return 1
		}
		fmt.Fprint(a.Out, FormatMembersTable(members))
		return 0
	case "add":
		name, email, role, err := parseMemberAddArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		member, err := a.MemberStore.AddMember(name, email, role)
		if err != nil {
			fmt.Fprintf(a.Err, "member add failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "added member: %s <%s> role=%s\n", member.Name, member.Email, member.Role)
		return 0
	case "enable", "disable":
		if len(args) != 2 {
			fmt.Fprintf(a.Err, "usage: cm member %s <email>\n", args[0])
			return 2
		}
		member, err := a.MemberStore.SetMemberEnabled(args[1], args[0] == "enable")
		if err != nil {
			fmt.Fprintf(a.Err, "member %s failed: %v\n", args[0], err)
			return 1
		}
		fmt.Fprintf(a.Out, "%s member: %s enabled=%t\n", args[0], member.Email, member.Enabled)
		return 0
	case "assign":
		appleEmail, memberEmail, relation, err := parseMemberAssignArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		assignment, err := a.MemberStore.AssignMember(appleEmail, memberEmail, relation)
		if err != nil {
			fmt.Fprintf(a.Err, "member assign failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "assigned member %s to %s as %s\n", memberEmail, assignment.AppleEmail, assignment.Relation)
		return 0
	case "unassign":
		appleEmail, memberEmail, _, err := parseMemberAssignArgs(args[1:])
		if err != nil {
			fmt.Fprintln(a.Err, err)
			return 2
		}
		if err := a.MemberStore.UnassignMember(appleEmail, memberEmail); err != nil {
			fmt.Fprintf(a.Err, "member unassign failed: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "unassigned member %s from %s\n", memberEmail, appleEmail)
		return 0
	default:
		fmt.Fprintf(a.Err, "unknown member command %q\n", args[0])
		return 2
	}
}

func parseMemberAddArgs(args []string) (name, email, role string, err error) {
	role = "operator"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", fmt.Errorf("--name requires a value")
			}
			name = args[i]
		case "--email":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", fmt.Errorf("--email requires a value")
			}
			email = args[i]
		case "--role":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", fmt.Errorf("--role requires a value")
			}
			role = args[i]
		default:
			return "", "", "", fmt.Errorf("unknown member add option %q", args[i])
		}
	}
	if name == "" || email == "" {
		return "", "", "", fmt.Errorf("usage: cm member add --name <name> --email <email> [--role <admin|operator|viewer>]")
	}
	return name, email, role, nil
}

func parseMemberAssignArgs(args []string) (appleEmail, memberEmail, relation string, err error) {
	relation = "owner"
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return "", "", "", fmt.Errorf("usage: cm member assign <apple-email> --member <member-email> [--relation owner]")
	}
	appleEmail = args[0]
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--member":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", fmt.Errorf("--member requires a value")
			}
			memberEmail = args[i]
		case "--relation":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", "", fmt.Errorf("--relation requires a value")
			}
			relation = args[i]
		default:
			return "", "", "", fmt.Errorf("unknown member assign option %q", args[i])
		}
	}
	if memberEmail == "" {
		return "", "", "", fmt.Errorf("--member is required")
	}
	return appleEmail, memberEmail, relation, nil
}

func FormatMembersTable(members []MemberWithAssignments) string {
	if len(members) == 0 {
		return "No members.\n"
	}
	rows := [][]string{{"EMAIL", "NAME", "ROLE", "ENABLED", "APPLE ACCOUNTS"}}
	for _, member := range members {
		accounts := make([]string, 0, len(member.Assignments))
		for _, assignment := range member.Assignments {
			accounts = append(accounts, assignment.AppleEmail+"("+assignment.Relation+")")
		}
		rows = append(rows, []string{
			member.Email,
			member.Name,
			member.Role,
			fmt.Sprintf("%t", member.Enabled),
			strings.Join(accounts, ", "),
		})
	}
	return formatRows(rows)
}

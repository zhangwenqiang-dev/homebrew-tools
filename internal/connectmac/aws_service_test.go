package connectmac

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMacPlanSelectsARMDefault(t *testing.T) {
	profile := validAWSProfile()
	profile.AWS.InstanceTypePriority = nil
	profile.AWS.AllowIntelFallback = false
	plan, err := BuildMacPlan(profile, time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildMacPlan returned error: %v", err)
	}
	if plan.ResourceName != "xcode-xcode-20260603-user@example.com" {
		t.Fatalf("resource name = %q", plan.ResourceName)
	}
	if plan.SelectedInstanceType != "mac2.metal" {
		t.Fatalf("selected instance type = %q, want mac2.metal", plan.SelectedInstanceType)
	}
	if plan.SelectedAMI != "ami-063755aadeb97329a" {
		t.Fatalf("selected ami = %q, want arm ami", plan.SelectedAMI)
	}
	for _, instanceType := range plan.InstanceTypePriority {
		if instanceType == "mac1.metal" {
			t.Fatal("default plan should exclude mac1.metal when intel fallback is disabled")
		}
	}
}

func TestBuildMacPlanSelectsIntelAMIWhenAllowed(t *testing.T) {
	profile := validAWSProfile()
	profile.AWS.AllowIntelFallback = true
	profile.AWS.InstanceTypePriority = []string{"mac1.metal"}
	plan, err := BuildMacPlan(profile, time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildMacPlan returned error: %v", err)
	}
	if plan.SelectedInstanceType != "mac1.metal" {
		t.Fatalf("selected instance type = %q, want mac1.metal", plan.SelectedInstanceType)
	}
	if plan.SelectedAMI != "ami-0538568e5d3653bea" {
		t.Fatalf("selected ami = %q, want x86 ami", plan.SelectedAMI)
	}
}

func TestFormatMacPlanIncludesSafetyDetails(t *testing.T) {
	plan, err := BuildMacPlan(validAWSProfile(), time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	text := FormatMacPlan(plan)
	for _, want := range []string{
		"AutoPlacement=off",
		"HostMaintenance=off",
		"cm-managed=true",
		"Elastic IP owner tag: Apple=user@example.com",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("plan text missing %q:\n%s", want, text)
		}
	}
}

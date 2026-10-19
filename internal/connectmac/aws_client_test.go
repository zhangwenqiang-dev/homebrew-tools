package connectmac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func TestRealAWSClientReleaseHostPreservesErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if got := r.Form.Get("Action"); got != "ReleaseHosts" {
			t.Fatalf("Action = %q, want ReleaseHosts", got)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<ReleaseHostsResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>request-id</requestId>
  <unsuccessful>
    <item>
      <resourceId>h-1</resourceId>
      <error>
        <code>Client.InvalidHost.Occupied</code>
        <message>host is occupied</message>
      </error>
    </item>
  </unsuccessful>
</ReleaseHostsResponse>`))
	}))
	defer server.Close()

	client := RealAWSClient{ec2: ec2.NewFromConfig(aws.Config{
		Region:      "us-west-2",
		Credentials: credentials.NewStaticCredentialsProvider("access-key", "secret-key", ""),
	}, func(options *ec2.Options) {
		options.BaseEndpoint = aws.String(server.URL)
	})}
	err := client.ReleaseHost(context.Background(), "h-1")
	if err == nil || !strings.Contains(err.Error(), "release host h-1: Client.InvalidHost.Occupied: host is occupied") {
		t.Fatalf("ReleaseHost() error = %v", err)
	}
	var apiErr interface {
		error
		ErrorCode() string
	}
	if !errors.As(err, &apiErr) {
		t.Fatalf("ReleaseHost() error = %T, want ErrorCode()", err)
	}
	if got := apiErr.ErrorCode(); got != "Client.InvalidHost.Occupied" {
		t.Fatalf("ErrorCode() = %q, want Client.InvalidHost.Occupied", got)
	}
}

func TestValidateReleaseHostOutput(t *testing.T) {
	tests := []struct {
		name string
		out  *ec2.ReleaseHostsOutput
		want string
		code string
	}{
		{name: "successful", out: &ec2.ReleaseHostsOutput{Successful: []string{"h-1"}}},
		{
			name: "occupied",
			out: &ec2.ReleaseHostsOutput{Unsuccessful: []ec2types.UnsuccessfulItem{{
				ResourceId: aws.String("h-1"),
				Error: &ec2types.UnsuccessfulItemError{
					Code:    aws.String("Client.InvalidHost.Occupied"),
					Message: aws.String("host is occupied"),
				},
			}}},
			want: "Client.InvalidHost.Occupied: host is occupied",
			code: "Client.InvalidHost.Occupied",
		},
		{name: "empty", out: &ec2.ReleaseHostsOutput{}, want: "did not confirm"},
		{
			name: "contradictory",
			out: &ec2.ReleaseHostsOutput{
				Successful:   []string{"h-1"},
				Unsuccessful: []ec2types.UnsuccessfulItem{{ResourceId: aws.String("h-1")}},
			},
			want: "contradictory",
		},
		{name: "nil", want: "did not confirm"},
		{name: "different successful host", out: &ec2.ReleaseHostsOutput{Successful: []string{"h-other"}}, want: "did not confirm"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateReleaseHostOutput("h-1", test.out)
			if test.want == "" {
				if err != nil {
					t.Fatalf("validateReleaseHostOutput() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReleaseHostOutput() error = %v, want containing %q", err, test.want)
			}
			if test.code != "" {
				var apiErr interface {
					error
					ErrorCode() string
				}
				if !errors.As(err, &apiErr) {
					t.Fatalf("validateReleaseHostOutput() error = %T, want ErrorCode()", err)
				}
				if got := apiErr.ErrorCode(); got != test.code {
					t.Fatalf("ErrorCode() = %q, want %q", got, test.code)
				}
			}
		})
	}
}

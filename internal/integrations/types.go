package integrations

type Method string

const (
	MethodOfficialHook Method = "official_hook"
	MethodSessionShim  Method = "session_shim"
	MethodWrapper      Method = "wrapper"
	MethodManual       Method = "manual"
)

type TargetStatus struct {
	Name              string
	DisplayName       string
	Detected          bool
	Path              string
	RecommendedMethod Method
	SupportedMethods  []Method
	Note              string
}

type DoctorResult struct {
	Name        string
	Command     string
	Detected    bool
	Path        string
	Recommended Method
	Installed   string
	Fallback    []Method
	Note        string
}

type InstallPlan struct {
	Target            string
	Detected          bool
	Path              string
	RecommendedMethod Method
	SelectedMethod    Method
	SupportedMethods  []Method
	Actions           []string
	SafeOption        string
	CanInstall        bool
	Note              string
	Scope             string
}

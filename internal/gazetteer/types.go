package gazetteer

import "time"

const (
	KindStreet        = "street"
	KindDistrict      = "district"
	KindNeighbourhood = "neighbourhood"
	KindMunicipality  = "municipality"
	KindTransit       = "transit"
	KindPark          = "park"
	KindSquare        = "square"
	KindLandmark      = "landmark"
	KindTrainStation  = "train_station"
	KindCommuterTrain = "commuter_train"
	KindSubwaySystem  = "subway_system"
)

type Entry struct {
	Name            string
	Kind            string
	Priority        int
	RequiresContext bool
	Sources         []EntrySource
}

type EntrySource struct {
	Key             string
	ExternalID      string
	Kind            string
	Priority        int
	RequiresContext bool
}

type SourceDefinition struct {
	Key         string
	DisplayName string
	URL         string
	License     string
	Attribution string
	MinimumRows int
	MaximumRows int
	MaximumSize int64
	Parse       func([]byte) ([]Entry, error)
}

type SourceSnapshot struct {
	Definition   SourceDefinition
	ContentHash  string
	ETag         string
	LastModified string
	FetchedAt    time.Time
	Entries      []Entry
}

type RefreshTrigger string

const (
	RefreshTriggerStartup   RefreshTrigger = "startup"
	RefreshTriggerScheduled RefreshTrigger = "scheduled"
	RefreshTriggerRetry     RefreshTrigger = "retry"
	RefreshTriggerManual    RefreshTrigger = "manual"
)

type SourceFetchDiagnostic struct {
	HTTPStatus   int
	ResponseSize int64
	RowCount     int
	Duration     time.Duration
	FailureStage string
}

type Status struct {
	ActiveGeneration int64
	EntryCount       int
	LastAttempt      time.Time
	LastSuccess      time.Time
	NextRefresh      time.Time
}

type RefreshResult struct {
	GenerationID int64
	EntryCount   int
	NotModified  bool
	Duration     time.Duration
}

type SourceStatus struct {
	Key                 string
	DisplayName         string
	URL                 string
	License             string
	Attribution         string
	ContentHash         string
	ContractVersion     string
	LastChecked         time.Time
	LastSuccess         time.Time
	ConsecutiveFailures int
	ActiveRowCount      int
	LastFailure         time.Time
	LastError           string
	LastFailureRunID    int64
	AttemptStatus       string
	AttemptRunID        int64
}

type KindCount struct {
	Kind  string
	Count int
}

type RefreshRunStatus struct {
	ID                     int64
	Trigger                string
	Status                 string
	StartedAt              time.Time
	CompletedAt            time.Time
	Duration               time.Duration
	ActiveGenerationBefore int64
	ActiveGenerationAfter  int64
	EntryCount             int
	Changed                *bool
	AllNotModified         *bool
	FailureStage           string
	FailedSourceKey        string
	ErrorMessage           string
	SourcesCompleted       int
	SourcesTotal           int
	SourcesFailed          int
}

type RefreshSourceStatus struct {
	RunID           int64
	Key             string
	Order           int
	DisplayName     string
	URL             string
	License         string
	Attribution     string
	ContractVersion string
	MinimumRows     int
	MaximumRows     int
	MaximumSize     int64
	Status          string
	StartedAt       time.Time
	CompletedAt     time.Time
	Duration        time.Duration
	HTTPStatus      int
	ResponseSize    int64
	RowCount        int
	ContentHash     string
	FailureStage    string
	ErrorMessage    string
}

type RefreshHistoryPage struct {
	Entries  []RefreshRunStatus
	Page     int
	HasNewer bool
	HasOlder bool
}

type RefreshRunDetails struct {
	Run     RefreshRunStatus
	Sources []RefreshSourceStatus
}

type GenerationStatus struct {
	ID            int64
	Status        string
	AggregateHash string
	CreatedAt     time.Time
	ActivatedAt   time.Time
	EntryCount    int
}

type OverrideStatus struct {
	Name   string
	Action string
	Reason string
}

type AdminSnapshot struct {
	Status      Status
	Sources     []SourceStatus
	Generations []GenerationStatus
	Overrides   []OverrideStatus
	KindCounts  []KindCount
	LatestRun   *RefreshRunStatus
}

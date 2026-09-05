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
}

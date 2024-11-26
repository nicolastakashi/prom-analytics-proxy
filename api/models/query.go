package models

type Response struct {
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

type Data struct {
	ResultType string `json:"resultType"`
	Stats      Stats  `json:"stats"`
}

type Stats struct {
	Timings Timings `json:"timings"`
	Samples Samples `json:"samples"`
}

type Timings struct {
	EvalTotalTime        float64 `json:"evalTotalTime"`
	ResultSortTime       float64 `json:"resultSortTime"`
	QueryPreparationTime float64 `json:"queryPreparationTime"`
	InnerEvalTime        float64 `json:"innerEvalTime"`
	ExecQueueTime        float64 `json:"execQueueTime"`
	ExecTotalTime        float64 `json:"execTotalTime"`
}

type Samples struct {
	TotalQueryableSamples int `json:"totalQueryableSamples"`
	PeakSamples           int `json:"peakSamples"`
}

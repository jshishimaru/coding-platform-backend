package models

type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ProblemTag represents the many-to-many join between problems and tags.
type ProblemTag struct {
	ProblemID int `json:"problem_id"`
	TagID     int `json:"tag_id"`
}

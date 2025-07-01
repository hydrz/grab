package gaodun

import (
	"fmt"
	"testing"
)

func TestClient(t *testing.T) {
	t.Skip("Skipping test for now, as it requires network access")

	api := NewApi()
	gStudyGradations, err := api.GStudy("33795")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	fmt.Printf("Glive Syllabus: %+v\n", gStudyGradations)

	gStudySyllabus, err := api.GStudySyllabus("33795", "49752")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	fmt.Printf("Glive Syllabus: %+v\n", *gStudySyllabus)

	epStudyGradations, err := api.EpStudy("17244")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	fmt.Printf("Ep Gradation: %+v\n", epStudyGradations)

	epStudySyllabus, err := api.EpStudySyllabus("17244", "21348")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	fmt.Printf("Ep Syllabus: %+v\n", epStudySyllabus)

	videoResource, err := api.VideoResource("628hgv1x0k1ffvYn", "SD", 0)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	fmt.Printf("Video Resource: %+v\n", videoResource)
}

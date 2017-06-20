package images

import (
	"reflect"
	"testing"
)

const (
	testImage = "docker.io/library/alpine:latest"
)

func TestImageMemberConfig(t *testing.T) {
	ctx, _, cs, manifest, expConfig, _, cleanup := setupImageStore(t)
	defer cleanup()

	image := Image{Name: testImage, Target: manifest}

	config, err := image.Config(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(config, expConfig) {
		t.Fatalf("config descriptors[%+v] not match to the expected[%+v]!", config, expConfig)
	}
}

func TestImageMemberRootFS(t *testing.T) {
	ctx, _, cs, manifest, _, _, cleanup := setupImageStore(t)
	defer cleanup()

	image := Image{Name: testImage, Target: manifest}

	diffIDs, err := image.RootFS(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(diffIDs, expDiffIDs) {
		t.Fatalf("diff ids descriptors[%+v] not match to the expected[%+v]!", diffIDs, expDiffIDs)
	}
}

func TestImageMemberSize(t *testing.T) {
	var expected int64 = 0
	ctx, _, cs, manifest, config, layers, cleanup := setupImageStore(t)
	defer cleanup()

	image := Image{Name: testImage, Target: manifest}
	size, err := image.Size(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	expected = manifest.Size + config.Size
	for _, layer := range layers {
		expected += layer.Size
	}
	if size != expected {
		t.Fatalf("image size[%d] not equal to the expected[%d]!", size, expected)
	}
}

func TestImageConfig(t *testing.T) {
	ctx, _, cs, image, expConfig, _, cleanup := setupImageStore(t)
	defer cleanup()

	config, err := Config(ctx, cs, image)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(config, expConfig) {
		t.Fatalf("config descriptors[%+v] not match to the expected[%+v]!", config, expConfig)
	}
}

func TestImageRootFS(t *testing.T) {
	ctx, _, cs, _, expConfig, _, cleanup := setupImageStore(t)
	defer cleanup()

	diffIDs, err := RootFS(ctx, cs, expConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(diffIDs, expDiffIDs) {
		t.Fatalf("diff ids descriptors[%+v] not match to the expected[%+v]!", diffIDs, expDiffIDs)
	}
}

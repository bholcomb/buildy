package cache

import (
	"path/filepath"
	"reflect"
	"testing"

	"buildy/internal/resource"
)

func TestParseMakeDepFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			// Finding 6: a header without an extension used to be filtered out.
			name:    "extensionless header",
			content: "obj/main.o: src/main.cpp src/config src/other.h\n",
			want:    []string{"src/main.cpp", "src/config", "src/other.h"},
		},
		{
			// S8: an escaped space is part of the path.
			name:    "escaped spaces",
			content: "main.o: main.cpp inc\\ dir/v.h\n",
			want:    []string{"main.cpp", "inc dir/v.h"},
		},
		{
			name:    "escaped hash and dollar",
			content: "main.o: main.cpp a\\#b.h cost$$.h\n",
			want:    []string{"main.cpp", "a#b.h", "cost$.h"},
		},
		{
			name:    "continuation lines",
			content: "obj/main.o: src/main.cpp \\\n  include/a.h \\\n  include/b.h\n",
			want:    []string{"src/main.cpp", "include/a.h", "include/b.h"},
		},
		{
			name:    "windows line endings",
			content: "main.o: main.cpp \\\r\n  a.h\r\n",
			want:    []string{"main.cpp", "a.h"},
		},
		{
			// -MP adds an empty rule per header; they add nothing.
			name:    "phony targets",
			content: "main.o: main.cpp a.h b.h\n\na.h:\n\nb.h:\n",
			want:    []string{"main.cpp", "a.h", "b.h"},
		},
		{
			// S8: the target separator is not the drive letter's colon.
			name:    "drive letters",
			content: "C:/proj/obj/main.o: C:/proj/src/main.cpp C:/proj/include/a.h\n",
			want:    []string{"C:/proj/src/main.cpp", "C:/proj/include/a.h"},
		},
		{
			name:    "backslash paths",
			content: "C:\\proj\\main.o: C:\\proj\\main.cpp C:\\proj\\a.h\n",
			want:    []string{"C:\\proj\\main.cpp", "C:\\proj\\a.h"},
		},
		{
			name:    "duplicates once",
			content: "main.o: main.cpp a.h a.h\n",
			want:    []string{"main.cpp", "a.h"},
		},
		{
			name:    "rule at end of input",
			content: "main.o:",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDepFile([]byte(tt.content), resource.DepFormatMake)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMakeDepFileRejectsGarbage(t *testing.T) {
	if _, err := parseDepFile([]byte("this is not a rule\n"), resource.DepFormatMake); err == nil {
		t.Error("expected an error for a line without a target separator")
	}
	if _, err := parseDepFile([]byte("x"), "nonsense"); err == nil {
		t.Error("expected an error for an unknown format")
	}
}

// MSVC: no extension filter, system directories still left out.
func TestParseMSVCDepFile(t *testing.T) {
	content := `{
  "Version": "1.2",
  "Data": {
    "Source": "c:\\proj\\main.cpp",
    "Includes": [
      "c:\\proj\\include\\config",
      "c:\\proj\\include\\a.h",
      "C:\\Program Files\\Microsoft Visual Studio\\VC\\include\\vector",
      "c:\\windows kits\\x.h"
    ]
  }
}`
	got, err := parseDepFile([]byte(content), resource.DepFormatMSVCJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.FromSlash("c:/proj/include/config"),
		filepath.FromSlash("c:/proj/include/a.h"),
		filepath.FromSlash("c:/windows kits/x.h"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

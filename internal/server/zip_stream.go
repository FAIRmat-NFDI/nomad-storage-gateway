package server

import (
	"archive/zip"
	"compress/flate"
	"io"
	"path"
	"strings"
)

func matchZipFiles(files []*zip.File, subpath string) (matched []*zip.File, singleFile bool) {
	cleanPath := strings.ReplaceAll(strings.Trim(subpath, "/\\"), "\\", "/")
	dirPrefix := cleanPath + "/"

	for _, file := range files {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		if file.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		// Case 1: subpath is a directory -> match prefix
		if strings.HasPrefix(name, dirPrefix) {
			matched = append(matched, file)
			continue
		}

		// Case 2: single file match
		if name == cleanPath {
			matched = append(matched, file)
			singleFile = true
		}
	}
	return matched, singleFile
}

func writeZipSubset(w io.Writer, files []*zip.File, subpath string, singleFile bool) error {
	zipWriter := zip.NewWriter(w)
	// Set DEFLATE compression level to 9 (flate.BestCompression). Matches NOMAD Python level.
	zipWriter.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})

	cleanPath := strings.ReplaceAll(strings.Trim(subpath, "/\\"), "\\", "/")
	dirPrefix := cleanPath + "/"

	for _, file := range files {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		var entryName string
		if singleFile {
			entryName = path.Base(name) // eg: OUTCAR
		} else {
			entryName = strings.TrimPrefix(name, dirPrefix) // eg: vasp/OUTCAR
		}
		if err := copyZipEntry(zipWriter, file, entryName); err != nil {
			return err
		}
	}
	return zipWriter.Close()
}

func copyZipEntry(dst *zip.Writer, src *zip.File, name string) error {
	if src.Method == zip.Deflate {
		// 1. Copy the source header and update the path name
		header := src.FileHeader
		header.Name = name
		target, err := dst.CreateRaw(&header)
		if err != nil {
			return err
		}
		// If source is already compressed (e.g. .gz, .png, or pre-deflated), pass it through raw:
		source, err := src.OpenRaw()
		if err != nil {
			return err
		}
		if closer, ok := source.(io.Closer); ok {
			defer closer.Close()
		}
		_, err = io.Copy(target, source)
		return err
	}

	// If source is uncompressed (ZIP_STORED text/XML), compress on the fly:
	target, err := dst.Create(name)
	if err != nil {
		return err
	}
	source, err := src.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	_, err = io.Copy(target, source)
	return err
}

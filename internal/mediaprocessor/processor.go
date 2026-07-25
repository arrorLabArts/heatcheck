package mediaprocessor

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Processor struct {
	clamAVAddress string
	minDuration   time.Duration
	maxDuration   time.Duration
	frameCount    int
	maxBytes      int64
}

type Config struct {
	ClamAVAddress string
	MinDuration   time.Duration
	MaxDuration   time.Duration
	FrameCount    int
	MaxBytes      int64
}

type Inspection struct {
	DurationSeconds float64
	Width           int
	Height          int
	VideoCodec      string
	Frames          [][]byte
	ProcessedVideo  []byte
	Thumbnail       []byte
}

type RejectedError struct {
	Reason string
}

func (e *RejectedError) Error() string {
	return e.Reason
}

func New(config Config) *Processor {
	return &Processor{
		clamAVAddress: config.ClamAVAddress,
		minDuration:   config.MinDuration,
		maxDuration:   config.MaxDuration,
		frameCount:    config.FrameCount,
		maxBytes:      config.MaxBytes,
	}
}

func (p *Processor) Ping(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", p.clamAVAddress)
	if err != nil {
		return fmt.Errorf("connect to ClamAV: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := connection.Write([]byte("zPING\x00")); err != nil {
		return fmt.Errorf("ping ClamAV: %w", err)
	}
	response := make([]byte, 64)
	count, err := connection.Read(response)
	if err != nil {
		return fmt.Errorf("read ClamAV ping: %w", err)
	}
	if strings.TrimSpace(strings.TrimRight(string(response[:count]), "\x00")) != "PONG" {
		return errors.New("ClamAV did not return PONG")
	}
	return nil
}

func (p *Processor) Process(
	ctx context.Context,
	source io.Reader,
	declaredSize int64,
) (Inspection, error) {
	directory, err := os.MkdirTemp("", "heatcheck-media-*")
	if err != nil {
		return Inspection{}, fmt.Errorf("create media workspace: %w", err)
	}
	defer os.RemoveAll(directory)

	inputPath := filepath.Join(directory, "input")
	file, err := os.OpenFile(inputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Inspection{}, fmt.Errorf("create media input: %w", err)
	}
	limit := p.maxBytes + 1
	if declaredSize > 0 && declaredSize < p.maxBytes {
		limit = declaredSize + 1
	}
	written, copyErr := io.Copy(file, io.LimitReader(source, limit))
	closeErr := file.Close()
	if copyErr != nil {
		return Inspection{}, fmt.Errorf("download media: %w", copyErr)
	}
	if closeErr != nil {
		return Inspection{}, fmt.Errorf("close media input: %w", closeErr)
	}
	if written > p.maxBytes || (declaredSize > 0 && written != declaredSize) {
		return Inspection{}, &RejectedError{Reason: "uploaded file size is invalid"}
	}

	if err := p.scan(ctx, inputPath); err != nil {
		return Inspection{}, err
	}
	inspection, err := p.probe(ctx, inputPath)
	if err != nil {
		return Inspection{}, err
	}
	duration := time.Duration(inspection.DurationSeconds * float64(time.Second))
	const durationTolerance = 250 * time.Millisecond
	if duration+durationTolerance < p.minDuration || duration-durationTolerance > p.maxDuration {
		return Inspection{}, &RejectedError{Reason: fmt.Sprintf(
			"video duration %.3fs is outside the allowed %.0f-%.0fs range",
			inspection.DurationSeconds,
			p.minDuration.Seconds(),
			p.maxDuration.Seconds(),
		)}
	}
	frames, err := p.extractFrames(ctx, inputPath, directory, inspection.DurationSeconds)
	if err != nil {
		return Inspection{}, err
	}
	inspection.Frames = frames
	inspection.Thumbnail = frames[0]
	processedVideo, err := p.transcode(ctx, inputPath, directory)
	if err != nil {
		return Inspection{}, err
	}
	inspection.ProcessedVideo = processedVideo
	return inspection, nil
}

func (p *Processor) scan(ctx context.Context, inputPath string) error {
	file, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open media for malware scan: %w", err)
	}
	defer file.Close()

	dialer := net.Dialer{Timeout: 10 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", p.clamAVAddress)
	if err != nil {
		return fmt.Errorf("connect to ClamAV: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))

	if _, err := connection.Write([]byte("zINSTREAM\x00")); err != nil {
		return fmt.Errorf("start ClamAV stream: %w", err)
	}
	buffer := make([]byte, 64*1024)
	var size [4]byte
	for {
		count, readErr := file.Read(buffer)
		if count > 0 {
			binary.BigEndian.PutUint32(size[:], uint32(count))
			if _, err := connection.Write(size[:]); err != nil {
				return fmt.Errorf("write ClamAV chunk size: %w", err)
			}
			if _, err := connection.Write(buffer[:count]); err != nil {
				return fmt.Errorf("write ClamAV chunk: %w", err)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read media for ClamAV: %w", readErr)
		}
	}
	if _, err := connection.Write([]byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("finish ClamAV stream: %w", err)
	}
	response, err := io.ReadAll(io.LimitReader(connection, 4096))
	if err != nil {
		return fmt.Errorf("read ClamAV result: %w", err)
	}
	result := strings.TrimSpace(strings.TrimRight(string(response), "\x00"))
	switch {
	case strings.HasSuffix(result, "OK"):
		return nil
	case strings.Contains(result, "FOUND"):
		return &RejectedError{Reason: "malware scan rejected the uploaded file"}
	default:
		return fmt.Errorf("unexpected ClamAV result: %s", result)
	}
}

func (p *Processor) probe(ctx context.Context, inputPath string) (Inspection, error) {
	command := exec.CommandContext(
		ctx,
		"ffprobe",
		"-v", "error",
		"-protocol_whitelist", "file,pipe",
		"-show_entries", "format=duration:stream=codec_type,codec_name,width,height",
		"-of", "json",
		inputPath,
	)
	output, err := command.Output()
	if err != nil {
		return Inspection{}, &RejectedError{Reason: "the uploaded file is not a readable video"}
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration json.Number `json:"duration"`
		} `json:"format"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	decoder.UseNumber()
	if err := decoder.Decode(&probe); err != nil {
		return Inspection{}, &RejectedError{Reason: "the uploaded video metadata is invalid"}
	}
	duration, err := probe.Format.Duration.Float64()
	if err != nil || duration <= 0 {
		return Inspection{}, &RejectedError{Reason: "the uploaded video has no valid duration"}
	}
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" && stream.Width > 0 && stream.Height > 0 {
			if stream.Width > 7680 || stream.Height > 4320 ||
				int64(stream.Width)*int64(stream.Height) > 33_177_600 {
				return Inspection{}, &RejectedError{Reason: "the uploaded video dimensions exceed the supported maximum"}
			}
			return Inspection{
				DurationSeconds: duration,
				Width:           stream.Width,
				Height:          stream.Height,
				VideoCodec:      stream.CodecName,
			}, nil
		}
	}
	return Inspection{}, &RejectedError{Reason: "the uploaded file has no video stream"}
}

func (p *Processor) extractFrames(
	ctx context.Context,
	inputPath string,
	directory string,
	durationSeconds float64,
) ([][]byte, error) {
	outputPattern := filepath.Join(directory, "frame-%03d.jpg")
	fps := float64(p.frameCount) / durationSeconds
	command := exec.CommandContext(
		ctx,
		"ffmpeg",
		"-v", "error",
		"-protocol_whitelist", "file,pipe",
		"-i", inputPath,
		"-vf", fmt.Sprintf("fps=%.8f,scale='min(768,iw)':-2", fps),
		"-frames:v", fmt.Sprintf("%d", p.frameCount),
		"-q:v", "3",
		outputPattern,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("extract video frames: %w: %s", err, strings.TrimSpace(string(output)))
	}
	paths, err := filepath.Glob(filepath.Join(directory, "frame-*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("list extracted frames: %w", err)
	}
	sort.Strings(paths)
	if len(paths) < 3 {
		return nil, &RejectedError{Reason: "the uploaded video did not contain enough decodable frames"}
	}
	frames := make([][]byte, 0, len(paths))
	for _, framePath := range paths {
		frame, err := os.ReadFile(framePath)
		if err != nil {
			return nil, fmt.Errorf("read extracted frame: %w", err)
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func (p *Processor) transcode(
	ctx context.Context,
	inputPath string,
	directory string,
) ([]byte, error) {
	outputPath := filepath.Join(directory, "processed.mp4")
	command := exec.CommandContext(
		ctx,
		"ffmpeg",
		"-v", "error",
		"-protocol_whitelist", "file,pipe",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map", "0:a?",
		"-vf", "scale='min(1080,iw)':-2",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-maxrate", "5M",
		"-bufsize", "10M",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		"-y",
		outputPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("transcode video: %w: %s", err, strings.TrimSpace(string(output)))
	}
	processed, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read transcoded video: %w", err)
	}
	if len(processed) == 0 || int64(len(processed)) > p.maxBytes {
		return nil, &RejectedError{Reason: "the standardized video output has an invalid size"}
	}
	return processed, nil
}

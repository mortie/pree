package main

import "path"
import "sort"
import "os"
import "io/ioutil"
import "bufio"
import "strconv"
import "fmt"
import "strings"
import "runtime"
import "flag"

type Options struct {
	ShowRSS bool
	ShowCPU bool
	Reverse bool
	SortFunc func(a *Process, b *Process) bool
}

type Process struct {
	Pid int
	PPid int
	RSS int
	CPU float32

	AccumRSS int
	AccumCPU float32
	PrettyName string
	Children []*Process
}

func (proc *Process) CalcAccumRSS() int {
	if proc.AccumRSS > 0 {
		return proc.AccumRSS
	}

	proc.AccumRSS = proc.RSS
	for _, child := range proc.Children {
		proc.AccumRSS += child.CalcAccumRSS()
	}

	return proc.AccumRSS
}

func (proc *Process) CalcAccumCPU() float32 {
	if proc.AccumCPU > 0 {
		return proc.AccumCPU
	}

	proc.AccumCPU = proc.CPU
	for _, child := range proc.Children {
		proc.AccumCPU += child.CalcAccumCPU()
	}

	return proc.AccumCPU
}

type SortProcs struct {
	Procs []*Process
	Reverse bool
	SortFunc func(a *Process, b *Process) bool
}

func (procs SortProcs) Len() int {
	return len(procs.Procs)
}

func (procs SortProcs) Less(i, j int) bool {
	less := procs.SortFunc(procs.Procs[i], procs.Procs[j])
	if procs.Reverse {
		return !less
	} else {
		return less
	}
}

func (procs SortProcs) Swap(i, j int) {
	tmp := procs.Procs[i]
	procs.Procs[i] = procs.Procs[j]
	procs.Procs[j] = tmp
}

type Processes map[int]*Process

func PrettySize(kib int) string {
	if kib < 1024 {
		return fmt.Sprintf("%dKiB", kib)
	} else if kib < 1024 * 1024 {
		return fmt.Sprintf("%.2fMiB", float32(kib) / float32(1024))
	} else {
		return fmt.Sprintf("%.2fGiB", float32(kib) / float32(1024 * 1024))
	}
}

func ShowProcess(proc *Process, opts *Options) string {
	if opts.ShowCPU && opts.ShowRSS {
		return fmt.Sprintf("(#%d; %s %.01f%%) -- %s %.01f%%",
			proc.Pid, PrettySize(proc.RSS), proc.CPU * 100,
			PrettySize(proc.CalcAccumRSS()), proc.CalcAccumCPU() * 100)
	} else if opts.ShowCPU {
		return fmt.Sprintf("(#%d; %.01f%%) -- %.01f%%",
			proc.Pid, proc.CPU * 100, proc.CalcAccumCPU() * 100)
	} else if opts.ShowRSS {
		return fmt.Sprintf("(#%d; %s -- %s",
			proc.Pid, PrettySize(proc.RSS), PrettySize(proc.CalcAccumRSS()))
	} else {
		return fmt.Sprintf("(#%d)", proc.Pid)
	}
}

func TicksSinceBoot() (int64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil { return 0, err }
	defer file.Close()
	r := bufio.NewReader(file)

	line, err := r.ReadString('\n')
	if err != nil { return 0, err }

	fields := strings.Fields(line)

	var total int64 = 0
	for _, val := range fields[1:] {
		num, err := strconv.ParseInt(val, 10, 64)
		if err != nil { return 0, err }
		total += num
	}

	return total / int64(runtime.NumCPU()), nil
}

func ReadProc(procs Processes, pid int) (*Process, error) {
	if proc, ok := procs[pid]; ok {
		return proc, nil
	}

	proc := &Process{Pid: pid}

	stat, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil { return nil, err }

	statParts := strings.Split(string(stat), ") ")
	statVals := strings.Split(statParts[1], " ")

	name := strings.Split(statParts[0], " (")[1]
	proc.PPid, err = strconv.Atoi(statVals[4-3])
	if err != nil { return nil, err }
	rssPages, err := strconv.Atoi(statVals[24-3])
	if err != nil { return nil, err }
	proc.RSS = (rssPages * os.Getpagesize()) / 1024;

	uTime, err := strconv.ParseInt(statVals[14-3], 10, 64)
	if err != nil { return nil, err }
	sTime, err := strconv.ParseInt(statVals[15-3], 10, 64)
	if err != nil { return nil, err }
	startTime, err := strconv.ParseInt(statVals[22-3], 10, 64)
	if err != nil { return nil, err }

	// Do this for every process, right after reading /proc/[pid]/stat,
	// for best accuracy
	totalTime, err := TicksSinceBoot()
	if err != nil { return nil, err }

	proc.CPU = float32(uTime + sTime) / (float32(totalTime) - float32(startTime))

	realPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		proc.PrettyName = name
	} else {
		proc.PrettyName = path.Base(realPath)
	}

	var parent *Process
	if proc.PPid != 0 {
		var ok bool
		parent, ok = procs[proc.PPid]
		if !ok {
			parent, err = ReadProc(procs, proc.PPid)
			if err != nil {
				return nil, err
			}
		}

		parent.Children = append(parent.Children, proc)
	}

	procs[pid] = proc
	return proc, nil
}

func ReadProcs(procs Processes) {
	files, err := ioutil.ReadDir("/proc")
	if err != nil { panic(err) }

	for _, file := range files {
		if (!file.IsDir()) { continue; }
		pid, err := strconv.ParseInt(file.Name(), 10, 32)
		if err != nil { continue; }

		_, err = ReadProc(procs, int(pid))
		if err != nil {
			fmt.Println(err)
		}
	}
}

func PrintFancyTree(proc *Process, opts *Options, prefix string, bar string, connector string) {
	// Don't want to show the "─╴" for the first process in the tree.
	// The length of the dashes also decides how much "padding" should be on the
	// prefix of child processes.
	var dashes string
	var subPadding string
	if prefix == ""  {
		dashes = ""
		subPadding = " "
	} else {
		dashes = "─╴"
		subPadding = "   "
	}

	// These formats are kind of a mess, but meh.
	var format string
	if len(proc.Children) > 0 {
		format = "%s%s%s%s ╤ %s\n"
	} else {
		format = "%s%s%s%s %s\n"
	}

	fmt.Printf(format, prefix, connector, dashes, proc.PrettyName, ShowProcess(proc, opts))

	sort.Stable(SortProcs{proc.Children, opts.Reverse, opts.SortFunc})
	subPrefix := prefix + bar + strings.Repeat(" ", len(proc.PrettyName)) + subPadding
	for i, child := range proc.Children {
		if i == len(proc.Children) - 1 {
			connector = "└"
			bar = " "
		} else {
			connector = "├"
			bar = "│"
		}

		PrintFancyTree(child, opts, subPrefix, bar, connector)
	}
}

func PrintFancyRoot(proc *Process, opts *Options) {
	PrintFancyTree(proc, opts, "", "", "")
}

func PrintBoringTree(proc *Process, opts *Options, prefix string) {
	format := "%s%s %s\n"
	fmt.Printf(format,
		prefix, proc.PrettyName, ShowProcess(proc, opts))

	sort.Stable(SortProcs{proc.Children, opts.Reverse, opts.SortFunc})
	subPrefix := prefix + strings.Repeat(" ", len(proc.PrettyName) + 1)
	for _, child := range proc.Children {
		PrintBoringTree(child, opts, subPrefix)
	}
}

func PrintBoringRoot(proc *Process, opts *Options) {
	PrintBoringTree(proc, opts, "")
}

func main() {
	showRSSFlag := flag.Bool("rss", true, "Show RSS")
	showCPUFlag := flag.Bool("cpu", true, "Show CPU")
	sortFlag := flag.String("sort", "rss", "Field to sort by (rss/cpu)")
	reverseFlag := flag.Bool("reverse", false, "Reverse sort direction")
	rootPidFlag := flag.Int("root", 1, "The PID to treat as the root of the process tree")
	styleFlag := flag.String("style", "auto", "Style (fancy|boring|auto)")
	flag.Parse()

	var opts Options
	opts.ShowRSS = *showRSSFlag
	opts.ShowCPU = *showCPUFlag
	opts.Reverse = *reverseFlag
	if *sortFlag == "rss" {
		opts.SortFunc = func(a *Process, b *Process) bool {
			return a.CalcAccumRSS() < b.CalcAccumRSS()
		}
	} else if *sortFlag == "cpu" {
		opts.SortFunc = func(a *Process, b *Process) bool {
			return a.CalcAccumCPU() < b.CalcAccumCPU()
		}
	} else {
		fmt.Printf("Unknown sort option: %s\n", *sortFlag)
		os.Exit(1)
	}

	procs := Processes{}
	ReadProcs(procs)
	rootProc, ok := procs[*rootPidFlag]
	if !ok {
		fmt.Printf("No PID %d!\n", *rootPidFlag)
		os.Exit(1)
	}

	ttyStat, err := os.Stdout.Stat()
	isTTY := false
	if err == nil {
		isTTY = (ttyStat.Mode() & os.ModeCharDevice) != 0
	}

	if *styleFlag == "fancy" || (*styleFlag == "auto" && isTTY) {
		PrintFancyRoot(rootProc, &opts)
	} else if *styleFlag == "boring" || (*styleFlag == "auto" && !isTTY) {
		PrintBoringRoot(rootProc, &opts)
	} else {
		fmt.Printf("Unknown style option: %s\n", *styleFlag)
		os.Exit(1)
	}
}

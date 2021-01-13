package main

import "path"
import "sort"
import "os"
import "io/ioutil"
import "bufio"
import "strconv"
import "fmt"
import "strings"

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

type AccumRSSSortProcs []*Process

func (procs AccumRSSSortProcs) Len() int {
	return len(procs)
}

func (procs AccumRSSSortProcs) Less(i, j int) bool {
	return procs[i].CalcAccumRSS() < procs[j].CalcAccumRSS()
}

func (procs AccumRSSSortProcs) Swap(i, j int) {
	tmp := procs[i]
	procs[i] = procs[j]
	procs[j] = tmp
}

type Processes map[int]*Process

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

	return total, nil
}

func PrettySize(kib int) string {
	if kib < 1024 {
		return fmt.Sprintf("%dKiB", kib)
	} else if kib < 1024 * 1024 {
		return fmt.Sprintf("%.2fMiB", float32(kib) / float32(1024))
	} else {
		return fmt.Sprintf("%.2fGiB", float32(kib) / float32(1024 * 1024))
	}
}

func ReadProc(procs Processes, pid int) (*Process, error) {
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

	proc.CPU = float32(uTime + sTime) / (float32(totalTime) - float32(startTime) * 2.5)

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

func PrintFancyTree(proc *Process, prefix string, bar string, connector string) {
	var format string
	if len(proc.Children) > 0 {
		format = "%s%s─╴%s ╤ (#%d; %s %.2f%%) -- %s\n"
	} else {
		format = "%s%s─╴%s (#%d; %s %.2f%%) -- %s\n"
	}

	fmt.Printf(format,
		prefix, connector, proc.PrettyName, proc.Pid,
		PrettySize(proc.RSS), proc.CPU * 100, PrettySize(proc.CalcAccumRSS()))

	sort.Stable(AccumRSSSortProcs(proc.Children))
	subPrefix := prefix + bar + strings.Repeat(" ", len(proc.PrettyName)) + "   "
	for i, child := range proc.Children {
		if i == len(proc.Children) - 1 {
			connector = "└"
			bar = " "
		} else {
			connector = "├"
			bar = "│"
		}

		PrintFancyTree(child, subPrefix, bar, connector)
	}
}

func PrintFancyRoot(proc *Process) {
	PrintFancyTree(proc, "", "", "")
}

func PrintBoringTree(proc *Process, prefix string) {
	format := "%s%s (#%d; %s %.2f%%) -- %s\n"
	fmt.Printf(format,
		prefix, proc.PrettyName, proc.Pid,
		PrettySize(proc.RSS), proc.CPU * 100, PrettySize(proc.CalcAccumRSS()))

	sort.Stable(AccumRSSSortProcs(proc.Children))
	subPrefix := prefix + strings.Repeat(" ", len(proc.PrettyName) + 1)
	for _, child := range proc.Children {
		PrintBoringTree(child, subPrefix)
	}
}

func PrintBoringRoot(proc *Process) {
	PrintBoringTree(proc, "")
}

func main() {
	procs := Processes{}
	ReadProcs(procs)
	pid1, ok := procs[1]
	if !ok {
		fmt.Println("No PID 1!")
		return
	}

	if fileInfo, _ := os.Stdout.Stat(); (fileInfo.Mode() & os.ModeCharDevice) != 0 {
		PrintFancyRoot(pid1)
	} else {
		PrintBoringRoot(pid1)
	}
}

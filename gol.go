package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type rowChans struct {
	input  chan uint8
	output chan uint8
}

type wChans struct {
	input         chan uint8
	output        chan uint8
	top           rowChans
	bottom        rowChans
	outputRequest chan bool
}

func receiveRow(width int, val <-chan byte) []byte {
	row := make([]byte, width)
	for x := 0; x < width; x++ {
		row[x] = <-val
	}
	return row
}

func sendOutput(p golParams, d distributorChans, world [][]byte, turn int) {
	// Request the io goroutine to write in the image with the given filename.
	d.io.command <- ioOutput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight), strconv.Itoa(turn)}, "x")

	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			d.io.outputVal <- world[y][x]
		}
	}
}

func calculateFinalAlive(p golParams, world [][]byte) []cell {
	// Create an empty slice to store coordinates of cells that are still alive after p.turns are done.
	var finalAlive []cell
	// Go through the world and append the cells that are still alive.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			if world[y][x] != 0 {
				finalAlive = append(finalAlive, cell{x: x, y: y})
			}
		}
	}

	return finalAlive
}

func worker(p golParams, chans wChans, sliceHeight int) {
	// Create a slice for holding an up-to-date version of the cells being acted on by the worker
	workerSlice := make([][]byte, sliceHeight)
	for i := range workerSlice {
		workerSlice[i] = make([]byte, p.imageWidth)
	}

	for i := 0; i < p.turns; i++ {
		// Receive top and bottom row
		workerSlice[0] = receiveRow(p.imageWidth, chans.top.input)
		workerSlice[sliceHeight-1] = receiveRow(p.imageWidth, chans.bottom.input)

		// Receive center section if first turn
		if i == 0 {
			for j := 1; j < sliceHeight-1; j++ {
				workerSlice[j] = receiveRow(p.imageWidth, chans.input)
			}
		}

		// Does the distributor want the workers to output the board?
		sendToDistributor := <-chans.outputRequest

		// Create temporary slice
		newSlice := make([][]byte, sliceHeight)
		for i := range newSlice {
			newSlice[i] = make([]byte, p.imageWidth)
		}

		// Process center and update workerSlice
		for y := 1; y < sliceHeight-1; y++ {
			for x := 0; x < p.imageWidth; x++ {
				s := workerSlice
				w := p.imageWidth
				// Neighbourhood calculation: x-1...x+1, y-1...y+1 excluding x, y
				aliveNeighbours := (int(s[y-1][(x-1+w)%w]) + int(s[y-1][x]) + int(s[y-1][(x+1)%w]) + int(s[y][(x-1+w)%w]) +
					int(s[y][(x+1)%w]) + int(s[y+1][(x-1+w)%w]) + int(s[y+1][x]) + int(s[y+1][(x+1)%w])) / 255

				newSlice[y][x] = workerSlice[y][x]

				// If cell is currently alive and doesn't have 2 or 3 neighbours, kill it.
				// If cell is dead and has 3 neighbours life begins.
				if workerSlice[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						newSlice[y][x] = 0x00
					}
				} else if aliveNeighbours == 3 {
					newSlice[y][x] = 0xFF
				}
				if sendToDistributor {
					chans.output <- newSlice[y][x]
				}
			}
		}
		// Update the workerSlice with the newly processed center section
		workerSlice = newSlice

		// Send rows
		for x := 0; x < p.imageWidth; x++ {
			chans.top.output <- workerSlice[1][x]
			chans.bottom.output <- workerSlice[sliceHeight-2][x]
		}
	}
}

func exitDistributor(p golParams, d distributorChans, world [][]byte, alive chan []cell, ticker *time.Ticker, turn int) {
	sendOutput(p, d, world, turn)
	ticker.Stop()

	// Make sure that the Io has finished any output before exiting.
	d.io.command <- ioCheckIdle
	<-d.io.idle

	// Return the coordinates of cells that are still alive.
	alive <- calculateFinalAlive(p, world)
}

func createWorkerChannels(imageWidth, wCount int, wHeights []int) []wChans {
	chans := make([]wChans, wCount)
	for i := 0; i < wCount; i++ {
		chans[i].input = make(chan byte, wHeights[i]+2)
		chans[i].output = make(chan byte, wHeights[i])
		chans[i].outputRequest = make(chan bool, 1)

		selfBottom := make(chan uint8, imageWidth)
		nextTop := make(chan uint8, imageWidth)
		chans[i].bottom.input = nextTop
		chans[i].bottom.output = selfBottom
		chans[(i+1)%wCount].top.input = selfBottom
		chans[(i+1)%wCount].top.output = nextTop
	}
	return chans
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p golParams, d distributorChans, alive chan []cell) {
	// Creates a new ticker used for keeping track of when to output the number of alive cells
	ticker := time.NewTicker(2 * time.Second)

	// Create the 2D slice to store the world.
	world := make([][]byte, p.imageHeight)
	for i := range world {
		world[i] = make([]byte, p.imageWidth)
	}

	// Request the io goroutine to read in the image with the given filename.
	d.io.command <- ioInput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight)}, "x")

	// The io goroutine sends the requested image byte by byte, in rows.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			val := <-d.io.inputVal
			if val != 0 {
				world[y][x] = val
			}
		}
	}

	// Calculate worker heights
	workerHeights := make([]int, p.threads)
	i := 0
	for j := 0; j < p.imageHeight; j++ {
		workerHeights[i]++
		i = (i + 1) % p.threads
	}

	// Create worker channels and start worker goroutines
	workerChannels := createWorkerChannels(p.imageWidth, p.threads, workerHeights)
	for i := 0; i < p.threads; i++ {
		go worker(p, workerChannels[i], workerHeights[i]+2)
	}

	// Calculate the new state of Game of Life after the given number of turns.
	for turn := 0; turn < p.turns; turn++ {
		// send rows to workers
		if turn == 0 {
			baseY := 0
			for i := 0; i < p.threads; i++ {
				// Work out y values of top and bottom rows to be sent
				yTop := (baseY - 1 + p.imageHeight) % p.imageHeight
				yBottom := (baseY + workerHeights[i]) % p.imageHeight
				for x := 0; x < p.imageWidth; x++ {
					workerChannels[i].top.input <- world[yTop][x]
					workerChannels[i].bottom.input <- world[yBottom][x]
				}
				// Send center rows
				for y := baseY; y < baseY+workerHeights[i]; y++ {
					for x := 0; x < p.imageWidth; x++ {
						workerChannels[i].input <- world[y][x]
					}
				}
				baseY += workerHeights[i]
			}
		}

		displayAlive := false
		requestBoardFromWorkers := turn == p.turns-1
		select {
		case <-ticker.C:
			requestBoardFromWorkers = true
			displayAlive = true
		default:
		}

		// Deal with input
		running := true
		for {
			select {
			case key := <-d.key:
				if key == 's' {
					// If 's' is pressed, generate a PGM file with the current state of the board.
					requestBoardFromWorkers = true
					sendOutput(p, d, world, turn)
				} else if key == 'q' {
					// If 'q' is pressed, generate a PGM file with the current state of the board and then terminate the program.
					requestBoardFromWorkers = true
					exitDistributor(p, d, world, alive, ticker, turn)
				} else if key == 'p' {
					if running {
						// If p is pressed, pause the processing and print the current turn that is being processed.
						fmt.Println("Pausing... turn =", turn)
					} else {
						// If p is pressed again resume the processing and print "Continuing"
						fmt.Println("Continuing")
					}
					running = !running
				}
			default:
			}
			// Conditional break makes the `for` act like a `do while`
			// This allows for nicely pausing processing while still allowing outputting and quitting
			if running {
				break
			}
		}

		// Tell workers whether they should send current board to distributor
		for i := 0; i < p.threads; i++ {
			workerChannels[i].outputRequest <- requestBoardFromWorkers
		}

		if requestBoardFromWorkers {
			// Receive rows from workers
			baseY := 0
			for i := 0; i < p.threads; i++ {
				for j := 0; j < workerHeights[i]; j++ {
					world[baseY+j] = receiveRow(p.imageWidth, workerChannels[i].output)
				}
				baseY += workerHeights[i]
			}
			if displayAlive {
				fmt.Println("Alive cells:", len(calculateFinalAlive(p, world)))
			}
		}
	}
	exitDistributor(p, d, world, alive, ticker, p.turns)
}

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type rowChans struct {
	input  chan []uint8
	output chan []uint8
}

type wChans struct {
	input         chan []uint8
	output        chan []uint8
	top           rowChans
	bottom        rowChans
	outputRequest chan bool
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
	workerSlice := make([][]byte, sliceHeight)
	for i := range workerSlice {
		workerSlice[i] = make([]byte, p.imageWidth)
	}

	for i := 0; i < p.turns; i++ {
		// Receive top and bottom row
		workerSlice[0] = <-chans.top.input
		workerSlice[sliceHeight-1] = <-chans.bottom.input

		// Receive center section if first turn
		if i == 0 {
			for j := 1; j < sliceHeight-1; j++ {
				workerSlice[j] = <-chans.input
			}
		}

		// sendToDistributor := <-chans.outputRequest
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
				aliveNeighbours := (int(s[y-1][(x-1+w)%w]) + int(s[y-1][x]) + int(s[y-1][(x+1)%w]) + int(s[y][(x-1+w)%w]) +
					int(s[y][(x+1)%w]) + int(s[y+1][(x-1+w)%w]) + int(s[y+1][x]) + int(s[y+1][(x+1)%w])) / 255

				newSlice[y][x] = workerSlice[y][x]
				if workerSlice[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						newSlice[y][x] = 0x00
					}
				} else if aliveNeighbours == 3 {
					newSlice[y][x] = 0xFF
				}
			}
			if sendToDistributor {
				chans.output <- newSlice[y]
			}
		}
		workerSlice = newSlice

		// Send rows
		chans.top.output <- workerSlice[1]
		chans.bottom.output <- workerSlice[sliceHeight-2]
	}
}

func exitDistributor(p golParams, d distributorChans, world [][]byte, alive chan []cell, ticker *time.Ticker) {
	// Stop the ticker
	ticker.Stop()

	// Make sure that the Io has finished any output before exiting.
	d.io.command <- ioCheckIdle
	<-d.io.idle

	// Signal to gameOfLife that we're done
	d.exit <- true

	// Return the coordinates of cells that are still alive.
	alive <- calculateFinalAlive(p, world)
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p golParams, d distributorChans, alive chan []cell) {
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
		i++
		i %= p.threads
	}

	// Create worker channels and start worker goroutines
	workerChannels := make([]wChans, p.threads)
	for i := 0; i < p.threads; i++ {
		workerChannels[i].input = make(chan []byte, workerHeights[i]+2)
		workerChannels[i].output = make(chan []byte, workerHeights[i])
		workerChannels[i].outputRequest = make(chan bool, 1)

		selfBottom := make(chan []uint8, 1)
		nextTop := make(chan []uint8, 1)
		workerChannels[i].bottom.input = nextTop
		workerChannels[i].bottom.output = selfBottom
		workerChannels[(i+1)%p.threads].top.input = selfBottom
		workerChannels[(i+1)%p.threads].top.output = nextTop
	}

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

				// Send top and bottom rows to workers
				workerChannels[i].top.input <- world[yTop]
				workerChannels[i].bottom.input <- world[yBottom]

				// Send center rows
				for y := baseY; y < baseY+workerHeights[i]; y++ {
					workerChannels[i].input <- world[y]
				}
				baseY += workerHeights[i]
			}
		}

		// Deal with input
		requestBoardFromWorkers := false
		running := true
		for {
			select {
			case key := <-d.key:
				if key == 's' || key == 'q' {
					requestBoardFromWorkers = true
					sendOutput(p, d, world, turn)
					if key == 'q' {
						exitDistributor(p, d, world, alive, ticker)
					}
				} else if key == 'p' {
					if running {
						fmt.Println("Pausing... turn =", turn)
					} else {
						fmt.Println("Continuing")
					}
					running = !running
				}
			default:
			}
			if running {
				break
			}
		}

		displayAlive := false
		select {
		case <-ticker.C:
			requestBoardFromWorkers = true
			displayAlive = true
		default:
			requestBoardFromWorkers = requestBoardFromWorkers || turn == p.turns-1
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
					world[baseY+j] = <-workerChannels[i].output
				}
				baseY += workerHeights[i]
			}
			if displayAlive {
				fmt.Println("Alive cells:", len(calculateFinalAlive(p, world)))
			}
		}
	}
	sendOutput(p, d, world, p.turns)
	exitDistributor(p, d, world, alive, ticker)
}

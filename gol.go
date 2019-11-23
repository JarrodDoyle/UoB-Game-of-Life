package main

import (
	"fmt"
	"strconv"
	"strings"
)

type wChans struct {
	input  chan uint8
	output chan uint8
}

func sumIntSlice(x []int) int {
	total := 0
	for _, i := range x {
		total += i
	}
	return total
}

func receiveRow(width int, val chan byte) []byte {
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
	workerSlice := make([][]byte, sliceHeight)
	for i := range workerSlice {
		workerSlice[i] = make([]byte, p.imageWidth)
	}

	for i := 0; i < p.turns; i++ {
		// Receive top row
		workerSlice[0] = receiveRow(p.imageWidth, chans.input)
		// Receive center section if first turn
		if i == 0 {
			for j := 1; j < sliceHeight-1; j++ {
				workerSlice[j] = receiveRow(p.imageWidth, chans.input)
			}
		}
		// Receive bottom row
		workerSlice[sliceHeight-1] = receiveRow(p.imageWidth, chans.input)

		// Create temporary slice
		newSlice := make([][]byte, sliceHeight)
		for i := range newSlice {
			newSlice[i] = make([]byte, p.imageWidth)
		}

		// Process center and update workerSlice
		for y := 1; y < sliceHeight-1; y++ {
			row := make([]byte, p.imageWidth)
			for x := 0; x < p.imageWidth; x++ {
				s := workerSlice
				w := p.imageWidth
				aliveNeighbours := (int(s[y-1][(x-1+w)%w]) + int(s[y-1][x]) + int(s[y-1][(x+1)%w]) + int(s[y][(x-1+w)%w]) +
					int(s[y][(x+1)%w]) + int(s[y+1][(x-1+w)%w]) + int(s[y+1][x]) + int(s[y+1][(x+1)%w])) / 255

				row[x] = workerSlice[y][x]
				if workerSlice[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						row[x] = 0x00
					}
				} else if aliveNeighbours == 3 {
					row[x] = 0xFF
				}
				chans.output <- row[x]
			}
			newSlice[y] = row
		}
		workerSlice = newSlice
	}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p golParams, d distributorChans, alive chan []cell) {
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

	// Create worker channels and initialise worker threads
	workerChannels := make([]wChans, p.threads)
	workerHeights := make([]int, p.threads)
	for i := 0; i < p.threads; i++ {
		var wChans wChans
		var workerHeight int
		if i == p.threads-1 {
			workerHeight = p.imageHeight - (p.threads-1)*(p.imageHeight/p.threads)
		} else {
			workerHeight = p.imageHeight / p.threads
		}
		workerHeights[i] = workerHeight
		wChans.input = make(chan byte, p.imageWidth*(workerHeight+2))
		wChans.output = make(chan byte, p.imageWidth*workerHeight)
		workerChannels[i] = wChans
		go worker(p, workerChannels[i], workerHeight+2)
	}

	// Calculate the new state of Game of Life after the given number of turns.
	for turn := 0; turn < p.turns; turn++ {
		// send rows to workers
		for i := 0; i < p.threads; i++ {
			// Send top row
			y := (sumIntSlice(workerHeights[:i]) - 1 + p.imageHeight) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i].input <- world[y][x]
			}
			// Send center rows if turn 0
			if turn == 0 {
				for y := sumIntSlice(workerHeights[:i]); y < sumIntSlice(workerHeights[:i+1]); y++ {
					for x := 0; x < p.imageWidth; x++ {
						workerChannels[i].input <- world[y][x]
					}
				}
			}
			// Send bottom row
			y = sumIntSlice(workerHeights[:i+1]) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i].input <- world[y][x]
			}
		}

		// Receive rows from workers
		baseY := 0
		for i := 0; i < p.threads; i++ {
			for j := 0; j < workerHeights[i]; j++ {
				world[baseY+j] = receiveRow(p.imageWidth, workerChannels[i].output)
			}
			baseY += workerHeights[i]
		}

		alive <- calculateFinalAlive(p, world)

		// Deal with input
		running := true
		for {
			select {
			case key := <-d.key:
				if key == 's' {
					sendOutput(p, d, world, turn)
				} else if key == 'p' {
					if running {
						fmt.Println("Pausing... turn =", turn)
					} else {
						fmt.Println("Continuing")
					}
					running = !running
				} else if key == 'q' {
					sendOutput(p, d, world, turn)
					d.io.command <- ioCheckIdle
					<-d.io.idle
					d.exit <- true
				}
			default:
			}
			if running {
				break
			}
		}
	}

	// Send output to PGM io
	sendOutput(p, d, world, p.turns)

	// Make sure that the Io has finished any output before exiting.
	d.io.command <- ioCheckIdle
	<-d.io.idle

	// Return the coordinates of cells that are still alive.
	alive <- calculateFinalAlive(p, world)

	// Signal to gameOfLife that we're done
	d.exit <- true
}

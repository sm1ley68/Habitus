import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, test, vi } from "vitest";
import { OwnerApiError } from "@/lib/api/owner";
import PhotoUploader from "./PhotoUploader";

const uploadPhotos = vi.fn();
const deletePhoto = vi.fn();

vi.mock("@/lib/api/owner", async () => {
  const actual = await vi.importActual<typeof import("@/lib/api/owner")>("@/lib/api/owner");
  return {
    ...actual,
    uploadPhotos: (...a: unknown[]) => uploadPhotos(...a),
    deletePhoto: (...a: unknown[]) => deletePhoto(...a),
  };
});

const jpeg = () => new File([new Uint8Array([255, 216, 255, 224])], "a.jpg", { type: "image/jpeg" });

beforeEach(() => {
  uploadPhotos.mockReset();
  deletePhoto.mockReset();
});

test("загружает выбранные файлы и показывает их", async () => {
  uploadPhotos.mockResolvedValue({ photos: ["/static/uploads/1/a.jpg"] });
  render(<PhotoUploader listingId="1" photos={[]} onChange={vi.fn()} />);

  await userEvent.upload(screen.getByLabelText(/добавить фото/i), jpeg());

  await waitFor(() => expect(uploadPhotos).toHaveBeenCalledWith("1", [expect.any(File)]));
});

test("отказ бэка показывается текстом, а не молча теряется", async () => {
  uploadPhotos.mockRejectedValue(new OwnerApiError("photo_too_large", "Фотография больше 10 МБ"));
  render(<PhotoUploader listingId="1" photos={[]} onChange={vi.fn()} />);

  await userEvent.upload(screen.getByLabelText(/добавить фото/i), jpeg());

  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/больше 10 МБ/));
});

test("у каждого фото есть доступное имя и кнопка удаления", async () => {
  deletePhoto.mockResolvedValue({ photos: [] });
  render(
    <PhotoUploader listingId="1" photos={["/static/uploads/1/a.jpg"]} onChange={vi.fn()} />,
  );

  await userEvent.click(screen.getByRole("button", { name: /удалить фото 1/i }));
  await waitFor(() => expect(deletePhoto).toHaveBeenCalledWith("1", "/static/uploads/1/a.jpg"));
});

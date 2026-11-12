// This file is fork code: the source host allowlist dialog of the
// fork-plugin-packs experiment's organization page.

import { useFormik } from "formik";
import * as Yup from "yup";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { FormField } from "#/components/FormField/FormField";
import { Loader } from "#/components/Loader/Loader";
import { Spinner } from "#/components/Spinner/Spinner";
import { Textarea } from "#/components/Textarea/Textarea";
import { getFormHelpers } from "#/utils/formUtils";

// Mirrors the server's checks in coderd/fork_plugin_packs.go, so a typo is
// caught before the request.
const maxHosts = 50;
const maxHostLength = 253;
const invalidHostCharacters = /[/:@?# ]/;

const parseHosts = (text: string) => [
	...new Set(
		text
			.split("\n")
			.map((line) => line.trim().toLowerCase())
			.filter(Boolean),
	),
];

const validationSchema = Yup.object({
	hosts: Yup.string().test("hosts", (text, context) => {
		const hosts = parseHosts(text ?? "");
		const invalid = hosts.find(
			(host) =>
				host.length > maxHostLength || invalidHostCharacters.test(host),
		);
		if (invalid) {
			return context.createError({
				message: `"${invalid}" must be a host name without a scheme, port or path.`,
			});
		}
		if (hosts.length > maxHosts) {
			return context.createError({
				message: `At most ${maxHosts} hosts are allowed.`,
			});
		}
		return true;
	}),
});

type SourcesFormProps = {
	hosts: readonly string[];
	error: unknown;
	isSaving: boolean;
	onSave: (hosts: string[]) => void;
	onClose: () => void;
};

const SourcesForm: React.FC<SourcesFormProps> = ({
	hosts,
	error,
	isSaving,
	onSave,
	onClose,
}) => {
	const form = useFormik({
		initialValues: { hosts: hosts.join("\n") },
		validationSchema,
		onSubmit: (values) => onSave(parseHosts(values.hosts)),
	});
	const getFieldHelpers = getFormHelpers(form, error);

	return (
		<form className="flex flex-col gap-4" onSubmit={form.handleSubmit} noValidate>
			<FormField
				field={getFieldHelpers("hosts")}
				label="Source hosts"
				description="One host name per line, for example github.com. An empty list blocks installs and updates."
				control={(controlProps) => (
					<Textarea
						{...form.getFieldProps("hosts")}
						{...controlProps}
						rows={6}
						placeholder="github.com"
						disabled={isSaving}
						autoFocus
					/>
				)}
			/>
			{Boolean(error) && <ErrorAlert error={error} />}
			<DialogFooter>
				<Button
					type="button"
					variant="outline"
					disabled={isSaving}
					onClick={onClose}
				>
					Cancel
				</Button>
				<Button type="submit" disabled={!form.isValid || isSaving}>
					<Spinner loading={isSaving} />
					Save
				</Button>
			</DialogFooter>
		</form>
	);
};

type ForkPluginPackSourcesDialogProps = {
	/** Undefined until the saved hosts load. */
	hosts: readonly string[] | undefined;
	loadError: unknown;
	saveError: unknown;
	isSaving: boolean;
	onSave: (hosts: string[]) => void;
	/** While isSaving, close requests are ignored. */
	onClose: () => void;
	onCloseAutoFocus?: (event: Event) => void;
};

export const ForkPluginPackSourcesDialog: React.FC<
	ForkPluginPackSourcesDialogProps
> = ({
	hosts,
	loadError,
	saveError,
	isSaving,
	onSave,
	onClose,
	onCloseAutoFocus,
}) => {
	let content: React.ReactNode;
	if (hosts) {
		content = (
			<SourcesForm
				hosts={hosts}
				error={saveError}
				isSaving={isSaving}
				onSave={onSave}
				onClose={onClose}
			/>
		);
	} else if (loadError) {
		content = <ErrorAlert error={loadError} />;
	} else {
		content = <Loader />;
	}

	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open && !isSaving) {
					onClose();
				}
			}}
		>
			<DialogContent onCloseAutoFocus={onCloseAutoFocus}>
				<DialogHeader>
					<DialogTitle>Plugin pack sources</DialogTitle>
					<DialogDescription>
						Coder installs and updates plugin packs only from Git repositories
						on these hosts. A pack from a removed host keeps working but can no
						longer be updated.
					</DialogDescription>
				</DialogHeader>
				{content}
			</DialogContent>
		</Dialog>
	);
};
